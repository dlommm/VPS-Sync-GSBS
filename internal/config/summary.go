package config

// Summary returns a log-safe snapshot of non-secret settings.
func (c Config) Summary() map[string]interface{} {
	return map[string]interface{}{
		"gsbs_db":         c.GSBSDB,
		"out_dir":         c.OutDir,
		"fetch_prod_db":   c.FetchProdDB,
		"prod_db_src":     redactSrc(c.ProdDBSrc),
		"run_pcgw_sync":   c.RunPCGWSync,
		"pcgw_sync_full":  c.PCGWSyncFull,
		"public_base":     c.PublicBase,
		"webhook_set":     c.WebhookURL != "",
		"gsbs_version":    c.GSBSVersion,
		"r2_bucket":       c.R2Bucket,
		"r2_prefix":       c.R2Prefix,
		"r2_endpoint":     c.R2Endpoint,
		"r2_configured":   c.R2Configured(),
		"r2_cleanup":      c.R2Cleanup,
		"r2_keep":         c.R2Keep,
		"log_file":        c.LogFile,
		"log_level":       c.LogLevel,
		"aws_key_present": c.R2AccessKey != "",
	}
}

func redactSrc(src string) string {
	if src == "" {
		return ""
	}
	for j, c := range src {
		if c == ':' && j > 0 {
			return src[:j+1] + "***"
		}
	}
	return "***"
}

// RedactProdSrc hides remote paths in logs.
func RedactProdSrc(src string) string { return redactSrc(src) }
