package logx

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	stderrRestore func()
	logFile       *os.File
)

// Options configures logging output.
type Options struct {
	File         string // append log path; empty = stderr only
	MirrorStderr bool   // also print to terminal when logging to file
	Level        string // debug, info, warn, error
}

// Setup configures zerolog and optionally tees stderr into the log file.
func Setup(opts Options) error {
	level, err := zerolog.ParseLevel(opts.Level)
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(level)

	var writers []io.Writer

	if opts.File != "" {
		if err := os.MkdirAll(filepath.Dir(opts.File), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(opts.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		logFile = f
		writers = append(writers, f)

		if err := teeStderr(f, opts.MirrorStderr); err != nil {
			f.Close()
			return err
		}
	}

	if len(writers) == 0 || opts.MirrorStderr || isTerminal(os.Stderr) {
		writers = append(writers, os.Stderr)
	}

	out := io.MultiWriter(writers...)
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        out,
		TimeFormat: time.RFC3339,
		NoColor:    true,
	}).With().Timestamp().Logger()

	if opts.File != "" {
		log.Info().Str("log_file", opts.File).Msg("logging initialized")
	}
	return nil
}

// Close releases log resources.
func Close() {
	if stderrRestore != nil {
		stderrRestore()
		stderrRestore = nil
	}
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

func teeStderr(f *os.File, mirror bool) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	orig := os.Stderr
	os.Stderr = w

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				_, _ = f.Write(buf[:n])
				if mirror {
					_, _ = orig.Write(buf[:n])
				}
			}
			if readErr != nil {
				break
			}
		}
	}()

	stderrRestore = func() {
		w.Close()
		os.Stderr = orig
		r.Close()
	}
	return nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// RunStart logs the beginning of a command run.
func RunStart(cmd string, fields map[string]interface{}) {
	e := log.Info().Str("event", "run.start").Str("command", cmd)
	for k, v := range fields {
		e = e.Interface(k, v)
	}
	e.Msg("run started")
}

// RunOK logs successful completion.
func RunOK(cmd string, elapsed time.Duration, fields map[string]interface{}) {
	e := log.Info().Str("event", "run.ok").Str("command", cmd).Dur("elapsed", elapsed)
	for k, v := range fields {
		e = e.Interface(k, v)
	}
	e.Msg("run completed successfully")
}

// RunFail logs failure before exit.
func RunFail(cmd string, elapsed time.Duration, err error) {
	log.Error().
		Str("event", "run.fail").
		Str("command", cmd).
		Dur("elapsed", elapsed).
		Err(err).
		Msg("run failed")
}

// StepStart marks a pipeline step.
func StepStart(step string, fields map[string]interface{}) {
	e := log.Info().Str("event", "step.start").Str("step", step)
	for k, v := range fields {
		e = e.Interface(k, v)
	}
	e.Msg("step started")
}

// StepOK marks step success.
func StepOK(step string, elapsed time.Duration, fields map[string]interface{}) {
	e := log.Info().Str("event", "step.ok").Str("step", step).Dur("elapsed", elapsed)
	for k, v := range fields {
		e = e.Interface(k, v)
	}
	e.Msg("step completed")
}

// StepFail marks step failure.
func StepFail(step string, elapsed time.Duration, err error) {
	log.Error().
		Str("event", "step.fail").
		Str("step", step).
		Dur("elapsed", elapsed).
		Err(err).
		Msg("step failed")
}

// FileInfo logs path and size.
func FileInfo(step, path string) {
	info, err := os.Stat(path)
	if err != nil {
		log.Warn().Str("step", step).Str("path", path).Err(err).Msg("file stat failed")
		return
	}
	log.Info().
		Str("step", step).
		Str("path", path).
		Int64("bytes", info.Size()).
		Msg("file ready")
}
