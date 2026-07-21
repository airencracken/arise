// Package resolversnapshot stores the compact, immutable repository fields
// needed to construct a dependency plan without decoding the full search index.
package resolversnapshot

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

var ErrIncompatible = errors.New("resolver snapshot: incompatible schema")

const (
	filename = "resolver.arise"
	magic    = "ARISE-RESOLVER"
	schema   = uint32(2)
)

type header struct {
	Magic  string
	Schema uint32
	Count  int
}

type Record struct {
	Repository, RepositoryPath                 string
	RepositoryPriority, OverlayIndex           int
	EAPIBanned, EAPIDeprecated                 bool
	Category, Package, Version                 string
	Depend, Rdepend, Bdepend, Idepend, Pdepend string
	SrcURI, Slot, Subslot, Keywords, Iuse      string
	License, RequiredUse, Restrict, EAPI       string
}

func Path(databasePath string) string { return filepath.Join(databasePath, filename) }

func Write(db *badger.DB, databasePath string, workers int) error {
	var records []Record
	if err := ingest.QueryRangeParallel(db, "pkg:", workers, func(m *metadata.PackageMetadata) error {
		if m.Complete() {
			records = append(records, fromMetadata(m))
		}
		return nil
	}); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(databasePath, ".resolver-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	buffer := bufio.NewWriterSize(temporary, 1<<20)
	encoder := gob.NewEncoder(buffer)
	if err := encoder.Encode(header{Magic: magic, Schema: schema, Count: len(records)}); err != nil {
		return err
	}
	for i := range records {
		if err := encoder.Encode(&records[i]); err != nil {
			return err
		}
	}
	if err := buffer.Flush(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, Path(databasePath)); err != nil {
		return err
	}
	keep = true
	return nil
}

func Read(databasePath string) ([]*metadata.PackageMetadata, error) {
	file, err := os.Open(Path(databasePath))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := gob.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	var hdr header
	if err := decoder.Decode(&hdr); err != nil {
		return nil, err
	}
	if hdr.Magic != magic || hdr.Schema != schema || hdr.Count < 0 {
		return nil, fmt.Errorf("%w: header %+v", ErrIncompatible, hdr)
	}
	result := make([]*metadata.PackageMetadata, 0, hdr.Count)
	for range hdr.Count {
		var record Record
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		result = append(result, record.metadata())
	}
	return result, nil
}

func fromMetadata(m *metadata.PackageMetadata) Record {
	return Record{
		Repository: m.Repository, RepositoryPath: m.RepositoryPath,
		RepositoryPriority: m.RepositoryPriority, OverlayIndex: m.OverlayIndex,
		EAPIBanned: m.EAPIBanned, EAPIDeprecated: m.EAPIDeprecated,
		Category: m.Category, Package: m.Package, Version: m.Version,
		Depend: m.DEPEND, Rdepend: m.RDEPEND, Bdepend: m.BDEPEND, Idepend: m.IDEPEND, Pdepend: m.PDEPEND,
		SrcURI: m.SRC_URI, Slot: m.SLOT, Subslot: m.Subslot, Keywords: m.KEYWORDS, Iuse: m.IUSE,
		License: m.LICENSE, RequiredUse: m.REQUIRED_USE, Restrict: m.RESTRICT, EAPI: m.EAPI,
	}
}

func (r Record) metadata() *metadata.PackageMetadata {
	return &metadata.PackageMetadata{
		Repository: r.Repository, RepositoryPath: r.RepositoryPath,
		RepositoryPriority: r.RepositoryPriority, OverlayIndex: r.OverlayIndex,
		EAPIBanned: r.EAPIBanned, EAPIDeprecated: r.EAPIDeprecated,
		Category: r.Category, Package: r.Package, Version: r.Version,
		DEPEND: r.Depend, RDEPEND: r.Rdepend, BDEPEND: r.Bdepend, IDEPEND: r.Idepend, PDEPEND: r.Pdepend,
		SRC_URI: r.SrcURI, SLOT: r.Slot, Subslot: r.Subslot, KEYWORDS: r.Keywords, IUSE: r.Iuse,
		LICENSE: r.License, REQUIRED_USE: r.RequiredUse, RESTRICT: r.Restrict, EAPI: r.EAPI,
	}
}
