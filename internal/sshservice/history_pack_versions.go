package sshservice

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/yrashk/arm/internal/historypack"
)

// These native versions bridge full off-lock verification and the later short
// mutation fence. Identity/size alone cannot certify unchanged verified bytes;
// ctime also detects a writer restoring the original mtime. This is consistency
// evidence, never settlement or permission to remove a different hot receipt.
type sourceArchivePackVersion struct {
	Receipt           historypack.Receipt
	Records, Manifest sourceHistoryVersion
}

func captureSourceArchivePackVersion(root string, r historypack.Receipt) (sourceArchivePackVersion, error) {
	packs, dir, e := openSourcePackIdentity(root, r)
	if e != nil {
		return sourceArchivePackVersion{}, e
	}
	defer packs.Close()
	defer dir.Close()
	records, e := historyVersionAt(dir, "records.bin", SourceHistoryMaxPackBytes)
	if e != nil {
		return sourceArchivePackVersion{}, e
	}
	manifest, e := historyVersionAt(dir, "manifest.json", SourceHistoryMaxPackBytes)
	if e != nil {
		return sourceArchivePackVersion{}, e
	}
	if e = checkSourcePackIdentityAt(packs, dir, root, r); e != nil {
		return sourceArchivePackVersion{}, e
	}
	return sourceArchivePackVersion{r, records, manifest}, nil
}
func (v sourceArchivePackVersion) check(root string) error {
	now, e := captureSourceArchivePackVersion(root, v.Receipt)
	if e != nil {
		return e
	}
	if now != v {
		return fmt.Errorf("verified source pack native version changed; hot evidence retained")
	}
	return nil
}
func verifySourceArchivePackVersion(ctx context.Context, root string, op SourceHistoryOperation) (sourceArchivePackVersion, error) {
	if op.Receipt == nil {
		return sourceArchivePackVersion{}, fmt.Errorf("archive lacks published receipt")
	}
	before, e := captureSourceArchivePackVersion(root, *op.Receipt)
	if e != nil {
		return before, e
	}
	if e = verifySourceArchivePack(ctx, root, op); e != nil {
		return before, e
	}
	return before, before.check(root)
}

type sourceCatalogVersion struct {
	Catalog          sourceHistoryVersion
	Packs            []sourceArchivePackVersion
	Names, PackNames []string
}

func captureSourceCatalogVersion(ctx context.Context, c *historyCatalog) (sourceCatalogVersion, error) {
	var v sourceCatalogVersion
	var e error
	v.Catalog, e = historyVersionBound(c.path, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil {
		return v, e
	}
	v.Names, v.PackNames, e = sourceCatalogNames(c.path)
	if e != nil {
		return v, e
	}
	// Fetch a complete bounded roster before materializing versions. Full semantic
	// validation still independently verifies this roster and every family payload.
	rows, e := c.db.QueryContext(ctx, `SELECT substr(id,1,33) FROM source_history_packs ORDER BY id LIMIT 257`)
	if e != nil {
		return v, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return v, e
		}
		ids = append(ids, id)
		if len(ids) > SourceHistoryMaxPacks {
			rows.Close()
			return v, fmt.Errorf("source catalog version roster exceeds bound")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return v, e
	}
	for _, id := range ids {
		if e = ctx.Err(); e != nil {
			return v, e
		}
		p, e := c.pack(ctx, id)
		if e != nil {
			return v, e
		}
		version, e := captureSourceArchivePackVersion(filepath.Join(c.path, "packs"), p.Receipt)
		if e != nil {
			return v, e
		}
		v.Packs = append(v.Packs, version)
	}
	return v, v.check(c.path)
}
func (v sourceCatalogVersion) check(path string) error {
	if e := v.checkNames(path); e != nil {
		return e
	}
	now, e := historyVersionBound(path, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil {
		return e
	}
	if now != v.Catalog {
		return fmt.Errorf("validated catalog native version changed; recovery remains pending")
	}
	for _, p := range v.Packs {
		if e = p.check(filepath.Join(path, "packs")); e != nil {
			return e
		}
	}
	// Recheck catalog after the bounded complete pack walk, never just before it.
	now, e = historyVersionBound(path, SourceHistoryCatalogName, SourceHistoryMaxCatalogBytes)
	if e != nil {
		return e
	}
	if now != v.Catalog {
		return fmt.Errorf("validated catalog native version changed; recovery remains pending")
	}
	return v.checkNames(path)
}

// Complete bounded name rosters are part of the off-lock closure witness too.
// An unfamiliar stage/pack must not appear after validation while the catalog
// and every previously known pack retain their native versions.
func sourceCatalogNames(path string) ([]string, []string, error) {
	d, e := privateDirectory(path)
	if e != nil {
		return nil, nil, e
	}
	defer d.Close()
	names, e := historyNames(d, 4)
	if e != nil {
		return nil, nil, e
	}
	packs, e := privateDirectory(filepath.Join(path, "packs"))
	if e != nil {
		return nil, nil, e
	}
	defer packs.Close()
	packNames, e := historyNames(packs, SourceHistoryMaxPacks+2)
	if e != nil {
		return nil, nil, e
	}
	if e = validateDirectory(d, path); e != nil {
		return nil, nil, e
	}
	if e = validateDirectory(packs, filepath.Join(path, "packs")); e != nil {
		return nil, nil, e
	}
	return names, packNames, nil
}
func (v sourceCatalogVersion) checkNames(path string) error {
	names, packNames, e := sourceCatalogNames(path)
	if e != nil {
		return e
	}
	if !slices.Equal(v.Names, names) || !slices.Equal(v.PackNames, packNames) {
		return fmt.Errorf("validated source history namespace roster changed; inspect retained evidence")
	}
	return nil
}
