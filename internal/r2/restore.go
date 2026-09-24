package r2

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// ErrNoDBBackup reports that the bucket holds no db-backup/ object to restore.
var ErrNoDBBackup = errors.New("no DB backup found under db-backup/")

// LatestDBBackupKey returns the newest db-backup/ object key.
//
// UploadDBBackup names objects gsbs-<UTC timestamp>.db.gz, so lexical order is
// chronological order and the last key is the newest. PruneDBBackups relies on
// the same property.
func (c *Client) LatestDBBackupKey(ctx context.Context) (string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(c.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String("db-backup/"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("list db backups: %w", err)
		}
		for _, o := range page.Contents {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
	}
	if len(keys) == 0 {
		return "", ErrNoDBBackup
	}
	sort.Strings(keys)
	return keys[len(keys)-1], nil
}

// RestoreDBBackup downloads a db-backup/ object and writes it, decompressed, to
// dest. An empty key restores the newest backup. It returns the key used and
// the size written.
//
// This is the counterpart to UploadDBBackup and the recovery path for a host
// that has been rebuilt: the weekly job had been pushing snapshots to R2 with
// nothing able to read them back, so the published mirror was recoverable only
// by hand.
func (c *Client) RestoreDBBackup(ctx context.Context, key, dest string) (string, int64, error) {
	if key == "" {
		latest, err := c.LatestDBBackupKey(ctx)
		if err != nil {
			return "", 0, err
		}
		key = latest
	}

	obj, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", key, err)
	}
	defer obj.Body.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", 0, err
	}

	// Decompress into a sibling temp file and rename, so an interrupted restore
	// cannot leave a half-written database where the pipeline expects a whole
	// one.
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".restore-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	gz, err := gzip.NewReader(obj.Body)
	if err != nil {
		return "", 0, fmt.Errorf("open %s as gzip: %w", key, err)
	}
	defer gz.Close()

	n, err := io.Copy(tmp, gz)
	if err != nil {
		return "", 0, fmt.Errorf("decompress %s: %w", key, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", 0, fmt.Errorf("install restored db: %w", err)
	}

	log.Info().Str("key", key).Int64("bytes", n).Str("dest", dest).Msg("R2 DB backup restored")
	return key, n, nil
}
