package r2

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/dlommm/vps-sync-gsbs/internal/config"
	"github.com/rs/zerolog/log"
)

type Client struct {
	s3     *s3.Client
	bucket string
	prefix string
}

func New(cfg config.Config) (*Client, error) {
	if !cfg.R2Configured() {
		return nil, fmt.Errorf("R2 not configured (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, R2_ENDPOINT, R2_BUCKET)")
	}
	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, _ ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{URL: cfg.R2Endpoint, SigningRegion: "auto"}, nil
	})
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.R2AccessKey, cfg.R2SecretKey, "")),
		awsconfig.WithEndpointResolverWithOptions(resolver),
		awsconfig.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("R2 client config: %w", err)
	}
	prefix := strings.Trim(cfg.R2Prefix, "/")
	log.Info().
		Str("bucket", cfg.R2Bucket).
		Str("prefix", prefix).
		Str("endpoint", cfg.R2Endpoint).
		Msg("R2 client ready")
	return &Client{
		s3:     s3.NewFromConfig(awsCfg),
		bucket: cfg.R2Bucket,
		prefix: prefix,
	}, nil
}

func (c *Client) key(name string) string {
	if c.prefix == "" {
		return name
	}
	return c.prefix + "/" + name
}

// UploadLive publishes manifest artifacts from outDir. The bundle and metadata
// go first; index.json goes last so a reader can never see a new index that
// points at a bundle that has not finished uploading. Returns the object keys
// uploaded.
func (c *Client) UploadLive(ctx context.Context, outDir string, manifestVersion int) ([]string, error) {
	type artifact struct {
		file, ct, cache string
	}
	first := []artifact{
		{"manifest.json.gz", "application/gzip", "public, max-age=600"},
		{"manifest.meta.json", "application/json", "public, max-age=600"},
	}
	last := artifact{"index.json", "application/json", "public, max-age=120"}

	var uploaded []string

	for _, a := range first {
		path := filepath.Join(outDir, a.file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			log.Info().Str("file", a.file).Msg("R2 upload skipped (file absent)")
			continue
		}
		key := c.key(a.file)
		if err := c.putFile(ctx, path, key, a.ct, a.cache); err != nil {
			return uploaded, fmt.Errorf("upload %s: %w", key, err)
		}
		uploaded = append(uploaded, key)
	}

	// Deltas are no longer published; remove any leftover object from the old
	// delta flow so stale data cannot be served (non-fatal, no-op once gone).
	delKey := c.key("manifest.delta.json.gz")
	if _, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(delKey),
	}); err != nil {
		log.Warn().Str("key", delKey).Err(err).Msg("R2 stale delta delete failed (non-fatal)")
	}

	key := c.key(last.file)
	if err := c.putFile(ctx, filepath.Join(outDir, last.file), key, last.ct, last.cache); err != nil {
		return uploaded, fmt.Errorf("upload %s: %w", key, err)
	}
	uploaded = append(uploaded, key)

	snap := fmt.Sprintf("archive/v%d-%s", manifestVersion, time.Now().UTC().Format("20060102-150405"))
	for _, name := range []string{"index.json", "manifest.json.gz"} {
		path := filepath.Join(outDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		archKey := snap + "/" + name
		if err := c.putFile(ctx, path, archKey, "application/octet-stream", "private, max-age=31536000"); err != nil {
			return uploaded, fmt.Errorf("archive upload %s: %w", archKey, err)
		}
		uploaded = append(uploaded, archKey)
	}

	log.Info().Int("count", len(uploaded)).Str("archive", snap).Msg("R2 upload complete")
	return uploaded, nil
}

func (c *Client) putFile(ctx context.Context, path, key, contentType, cacheControl string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(c.bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(body),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String(cacheControl),
	})
	if err != nil {
		return err
	}
	log.Info().
		Str("key", key).
		Int("bytes", len(body)).
		Str("content_type", contentType).
		Msg("R2 object uploaded")
	return nil
}

// PruneArchives deletes oldest archive/v* snapshots beyond keep count.
func (c *Client) PruneArchives(ctx context.Context, keep int) (int, error) {
	prefix := "archive/"
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(c.s3, &s3.ListObjectsV2Input{
		Bucket:    aws.String(c.bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, fmt.Errorf("list archives: %w", err)
		}
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix != nil {
				keys = append(keys, strings.TrimSuffix(*cp.Prefix, "/"))
			}
		}
	}
	sort.Strings(keys)
	log.Info().Int("total", len(keys)).Int("keep", keep).Msg("R2 archive inventory")
	if len(keys) <= keep {
		return 0, nil
	}
	prune := len(keys) - keep
	for i := 0; i < prune; i++ {
		log.Info().Str("archive", keys[i]).Msg("R2 pruning archive")
		if err := c.deletePrefix(ctx, keys[i]+"/"); err != nil {
			return i, fmt.Errorf("prune %s: %w", keys[i], err)
		}
	}
	return prune, nil
}

func (c *Client) deletePrefix(ctx context.Context, prefix string) error {
	paginator := s3.NewListObjectsV2Paginator(c.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		if len(page.Contents) == 0 {
			continue
		}
		var objs []types.ObjectIdentifier
		for _, o := range page.Contents {
			objs = append(objs, types.ObjectIdentifier{Key: o.Key})
		}
		_, err = c.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(c.bucket),
			Delete: &types.Delete{Objects: objs},
		})
		if err != nil {
			return err
		}
	}
	return nil
}
