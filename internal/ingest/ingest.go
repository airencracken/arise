package ingest

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

const (
	keyPrefix    = "pkg:"
	maxBatchSize = 250
)

// OpenDB opens or creates a BadgerDB database at the given path.
// Callers are responsible for closing the returned DB via db.Close().
func OpenDB(path string) (*badger.DB, error) {
	opts := badger.DefaultOptions(path).
		WithValueLogFileSize(1 << 30).
		WithValueThreshold(1 << 20)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("ingest: open db: %w", err)
	}
	return db, nil
}

// Ingest reads PackageMetadata entries from the channel and writes them into
// the BadgerDB using batched writes. It returns the total number of entries
// ingested.
func Ingest(db *badger.DB, entries <-chan *metadata.PackageMetadata) (int, error) {
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
		}
	}

	if batchCount > 0 {
		if err := wb.Flush(); err != nil {
			return count, fmt.Errorf("ingest: final flush: %w", err)
		}
	}

	return count, nil
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

func encodeKey(cp string) []byte {
	return []byte(keyPrefix + cp)
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
