package ingest

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

const (
	keyPrefix         = "pkg:"
	fingerprintPrefix = "fp:"
	maxBatchSize      = 250
)

type ReconcileStats struct {
	Seen      int
	Changed   int
	Unchanged int
	Removed   int
}

// OpenDB opens or creates a BadgerDB database at the given path.
// Callers are responsible for closing the returned DB via db.Close().
func OpenDB(path string) (*badger.DB, error) {
	opts := badger.DefaultOptions(path).
		WithValueLogFileSize(1 << 30).
		WithValueThreshold(1 << 20).
		WithLoggingLevel(badger.WARNING)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("ingest: open db: %w", err)
	}
	return db, nil
}

// ResetPackageIndex removes all indexed package metadata while preserving any
// future non-package keys stored in the same database.
func ResetPackageIndex(db *badger.DB) error {
	if err := db.DropPrefix([]byte(keyPrefix)); err != nil {
		return fmt.Errorf("ingest: reset package index: %w", err)
	}
	if err := db.DropPrefix([]byte(fingerprintPrefix)); err != nil {
		return fmt.Errorf("ingest: reset fingerprint index: %w", err)
	}
	return nil
}

// Ingest reads PackageMetadata entries from the channel and writes them into
// the BadgerDB using batched writes. It returns the total number of entries
// ingested.
func Ingest(db *badger.DB, entries <-chan *metadata.PackageMetadata) (int, error) {
	return IngestWithProgress(db, entries, nil)
}

// IngestWithProgress is like Ingest and reports the committed entry count
// after each batch. The callback runs synchronously and should return quickly.
func IngestWithProgress(db *badger.DB, entries <-chan *metadata.PackageMetadata, progress func(int)) (int, error) {
	count := 0
	batchCount := 0
	wb := db.NewWriteBatch()
	defer wb.Cancel()

	for entry := range entries {
		if entry == nil {
			continue
		}

		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(entry); err != nil {
			return count, fmt.Errorf("ingest: encode %s: %w", entry.Key(), err)
		}

		key := encodeKey(entry.Key())
		val := buf.Bytes()

		if len(val) > 64<<20 {
			continue
		}

		if err := wb.Set(key, val); err != nil {
			return count, fmt.Errorf("ingest: write batch set %s (%d bytes): %w", entry.Key(), len(val), err)
		}

		batchCount++
		count++

		if batchCount >= maxBatchSize {
			if err := wb.Flush(); err != nil {
				return count, fmt.Errorf("ingest: flush: %w", err)
			}
			wb.Cancel()
			wb = db.NewWriteBatch()
			batchCount = 0
			if progress != nil {
				progress(count)
			}
		}
	}

	if batchCount > 0 {
		if err := wb.Flush(); err != nil {
			return count, fmt.Errorf("ingest: final flush: %w", err)
		}
	}
	if progress != nil {
		progress(count)
	}

	return count, nil
}

// ReconcileWithProgress incrementally updates changed package records and
// returns the set of keys observed in the source snapshot. Call RemoveMissing
// only when the source walk completed without errors.
func ReconcileWithProgress(db *badger.DB, entries <-chan *metadata.PackageMetadata, progress func(int)) (ReconcileStats, map[string]struct{}, error) {
	stats := ReconcileStats{}
	seen := make(map[string]struct{})
	selected := make(map[string]*metadata.PackageMetadata)
	for entry := range entries {
		if entry == nil {
			continue
		}
		stats.Seen++
		key := entry.Key()
		seen[key] = struct{}{}
		if current := selected[key]; current == nil || preferMetadata(entry, current) {
			selected[key] = entry
		}
		if progress != nil {
			progress(stats.Seen)
		}
	}
	existingFingerprints, err := loadFingerprints(db)
	if err != nil {
		return stats, seen, fmt.Errorf("load fingerprint index: %w", err)
	}
	wb := db.NewWriteBatch()
	defer wb.Cancel()
	batchCount := 0

	flush := func() error {
		if batchCount == 0 {
			return nil
		}
		if err := wb.Flush(); err != nil {
			return err
		}
		wb.Cancel()
		wb = db.NewWriteBatch()
		batchCount = 0
		return nil
	}

	for key, entry := range selected {
		fingerprint, err := metadata.Fingerprint(entry)
		if err != nil {
			return stats, seen, fmt.Errorf("fingerprint %s: %w", key, err)
		}
		existingFingerprint := existingFingerprints[key]
		if existingFingerprint == fingerprint {
			stats.Unchanged++
		} else {
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(entry); err != nil {
				return stats, seen, fmt.Errorf("encode %s: %w", key, err)
			}
			if buf.Len() > 64<<20 {
				continue
			}
			if err := wb.Set(encodeKey(key), buf.Bytes()); err != nil {
				return stats, seen, fmt.Errorf("write batch set %s: %w", key, err)
			}
			if err := wb.Set(encodeFingerprintKey(key), fingerprint[:]); err != nil {
				return stats, seen, fmt.Errorf("write fingerprint %s: %w", key, err)
			}
			batchCount++
			stats.Changed++
			if batchCount >= maxBatchSize {
				if err := flush(); err != nil {
					return stats, seen, fmt.Errorf("flush incremental batch: %w", err)
				}
			}
		}
	}
	if err := flush(); err != nil {
		return stats, seen, fmt.Errorf("final incremental flush: %w", err)
	}
	return stats, seen, nil
}

func preferMetadata(candidate, current *metadata.PackageMetadata) bool {
	if candidate.OverlayIndex != current.OverlayIndex {
		return candidate.OverlayIndex > current.OverlayIndex
	}
	candidateVersion, _ := atom.ParseVersion(candidate.Version)
	currentVersion, _ := atom.ParseVersion(current.Version)
	if candidateVersion == nil {
		if currentVersion == nil {
			if candidate.Version != current.Version {
				return candidate.Version > current.Version
			}
			return preferMetadataFingerprint(candidate, current)
		}
		return false
	}
	if currentVersion == nil {
		return true
	}
	if comparison := candidateVersion.Compare(currentVersion); comparison != 0 {
		return comparison > 0
	}
	return preferMetadataFingerprint(candidate, current)
}

func preferMetadataFingerprint(candidate, current *metadata.PackageMetadata) bool {
	candidateFingerprint, candidateErr := metadata.Fingerprint(candidate)
	currentFingerprint, currentErr := metadata.Fingerprint(current)
	if candidateErr != nil || currentErr != nil {
		return false
	}
	return bytes.Compare(candidateFingerprint[:], currentFingerprint[:]) > 0
}

// RemoveMissing deletes indexed packages absent from a successfully completed
// source walk.
func RemoveMissing(db *badger.DB, seen map[string]struct{}) (int, error) {
	var stale [][]byte
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefix)
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.ValidForPrefix(opts.Prefix); it.Next() {
			key := it.Item().KeyCopy(nil)
			cp := strings.TrimPrefix(string(key), keyPrefix)
			if _, ok := seen[cp]; !ok {
				stale = append(stale, key)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan stale packages: %w", err)
	}
	wb := db.NewWriteBatch()
	defer wb.Cancel()
	for _, key := range stale {
		if err := wb.Delete(key); err != nil {
			return 0, fmt.Errorf("delete stale package: %w", err)
		}
		cp := strings.TrimPrefix(string(key), keyPrefix)
		if err := wb.Delete(encodeFingerprintKey(cp)); err != nil {
			return 0, fmt.Errorf("delete stale fingerprint: %w", err)
		}
	}
	if len(stale) > 0 {
		if err := wb.Flush(); err != nil {
			return 0, fmt.Errorf("flush stale packages: %w", err)
		}
	}
	return len(stale), nil
}

// Query retrieves a PackageMetadata by its category/package key.
// Returns nil if no entry is found.
func Query(db *badger.DB, cp string) (*metadata.PackageMetadata, error) {
	var m *metadata.PackageMetadata

	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(encodeKey(cp))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil
			}
			return fmt.Errorf("get: %w", err)
		}
		return item.Value(func(val []byte) error {
			var err error
			m, err = decodeValue(val)
			return err
		})
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: query: %w", err)
	}

	return m, nil
}

// QueryRange iterates over all entries whose key matches the given prefix.
// The callback fn is called for each decoded entry. Iteration stops early if
// fn returns io.EOF.
func QueryRange(db *badger.DB, prefix string, fn func(*metadata.PackageMetadata) error) error {
	return db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				m, decodeErr := decodeValue(val)
				if decodeErr != nil {
					return decodeErr
				}
				return fn(m)
			})
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

// QueryKeys iterates over package keys without loading or decoding their values.
// The callback receives canonical category/package strings.
func QueryKeys(db *badger.DB, prefix string, fn func(string) error) error {
	return db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			key := string(it.Item().Key())
			if err := fn(strings.TrimPrefix(key, keyPrefix)); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

func encodeKey(cp string) []byte {
	return []byte(keyPrefix + cp)
}

func encodeFingerprintKey(cp string) []byte {
	return []byte(fingerprintPrefix + cp)
}

func loadFingerprints(db *badger.DB) (map[string][32]byte, error) {
	fingerprints := make(map[string][32]byte)
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(fingerprintPrefix)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.ValidForPrefix(opts.Prefix); it.Next() {
			cp := strings.TrimPrefix(string(it.Item().Key()), fingerprintPrefix)
			if err := it.Item().Value(func(value []byte) error {
				if len(value) != 32 {
					return fmt.Errorf("invalid fingerprint length %d for %s", len(value), cp)
				}
				var fingerprint [32]byte
				copy(fingerprint[:], value)
				fingerprints[cp] = fingerprint
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return fingerprints, err
}

func decodeValue(val []byte) (*metadata.PackageMetadata, error) {
	buf := bytes.NewBuffer(val)
	dec := gob.NewDecoder(buf)
	var m metadata.PackageMetadata
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &m, nil
}

func DecodeEntry(val []byte) (*metadata.PackageMetadata, error) {
	return decodeValue(val)
}
