// Package cli implements the apib-to-oas command-line interface using cobra.
//
// This package only parses arguments and wires together the drafter
// runner and the converter. The actual work lives in internal/drafter
// and internal/convert.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alecoletti/apib-to-oas/internal/convert"
	"github.com/alecoletti/apib-to-oas/internal/drafter"
)

// App is the apib-to-oas CLI. Stdout/Stderr/Stdin are injectable so the
// command tree can be tested without touching real files.
type App struct {
	Version   string
	Commit    string
	BuildTime string
	Stdout    io.Writer
	Stderr    io.Writer
	Stdin     io.Reader
}

// New returns an App wired to the process's standard streams.
func New(version, commit, buildTime string) *App {
	return &App{
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Stdin:     os.Stdin,
	}
}

// Exit codes:
//
//	1 = generic / IO failure
//	2 = APIB parse error (or --strict warnings)
//	3 = OAS conversion / marshal failure
const (
	ExitGeneric    = 1
	ExitParseError = 2
	ExitConvertErr = 3
)

// noColor is set globally by --no-colour or the NO_COLOUR env var.
var noColor bool

// Run builds the cobra command tree and runs it against args.
func (a *App) Run(ctx context.Context, args []string) error {
	if a.Stdin == nil {
		a.Stdin = os.Stdin
	}
	root := a.newRootCmd()
	root.SetArgs(args)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SilenceUsage = true
	root.SilenceErrors = true
	return root.ExecuteContext(ctx)
}

func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "apib-to-oas",
		Short: "Convert API Blueprint specifications to OpenAPI (3.0 / 3.1 / 3.2)",
		Long: `apib-to-oas converts API Blueprint (.apib) documents - including MSON - into
OpenAPI documents. Drafter binaries are embedded per-platform via go:embed,
so a single ` + "`apib-to-oas`" + ` binary works without external tools.`,
		Example: `  # Convert to YAML on stdout
  apib-to-oas convert spec.apib

  # Read from stdin, write JSON to a file (format inferred from extension)
  cat spec.apib | apib-to-oas convert - -o build/openapi.json

  # Target OpenAPI 3.1 with a security sidecar
  apib-to-oas convert spec.apib --oas-version 3.1 --security-config apib-to-oas-security.json

  # Just lint the APIB (no conversion); fail on warnings too
  apib-to-oas lint spec.apib --strict`,
		Version:       a.Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().BoolVar(&noColor, "no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colour in stderr diagnostics")

	root.AddCommand(a.newConvertCmd())
	root.AddCommand(a.newLintCmd())
	root.AddCommand(a.newVersionCmd())
	return root
}

func (a *App) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print the apib-to-oas version",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			commit := a.Commit
			if commit == "" {
				commit = "none"
			}
			buildTime := a.BuildTime
			if buildTime == "" {
				buildTime = "unknown"
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s (commit %s, built %s)\n", a.Version, commit, buildTime)
			return err
		},
	}
}

func (a *App) newConvertCmd() *cobra.Command {
	var (
		out            string
		format         string
		oasVersion     string
		infoVersion    string
		strict         bool
		quiet          bool
		securityConfig string
	)
	cmd := &cobra.Command{
		Use:     "convert <input.apib | ->",
		Aliases: []string{"c"},
		Short:   "Convert an API Blueprint document to OpenAPI",
		Long: `Convert reads an API Blueprint document from a path (or "-" for stdin),
parses it through the embedded Drafter, translates the API Elements AST
into an OpenAPI document, and writes it to stdout (or --output).

The output format is inferred from --output's extension when --format is
left at its default. Use --format explicitly when writing to stdout or to
a file with an unrecognised extension.`,
		Example: `  apib-to-oas convert spec.apib
  apib-to-oas convert spec.apib -o openapi.yaml
  apib-to-oas convert spec.apib -o openapi.json          # format inferred
  cat spec.apib | apib-to-oas convert - --format json
  apib-to-oas convert spec.apib --oas-version 3.2 --info-version $(git describe)`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConvert(cmd.Context(), args[0], out, format, oasVersion, infoVersion, strict, quiet, securityConfig)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "-", "output file (`-` for stdout); format inferred from extension")
	cmd.Flags().StringVarP(&format, "format", "f", "", "output format: yaml | json (default: yaml, or inferred from --output)")
	cmd.Flags().StringVar(&oasVersion, "oas-version", "3.0.3", "OpenAPI version: 3.0 (=3.0.3), 3.1 (=3.1.0), or 3.2 (=3.2.0)")
	cmd.Flags().StringVar(&infoVersion, "info-version", "", "override info.version (e.g. from CI); falls back to APIB metadata `VERSION:` then 0.0.0")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero if Drafter emits any warning (errors always fail)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress Drafter warning/note diagnostics on stderr (errors always shown)")
	cmd.Flags().StringVar(&securityConfig, "security-config", "", "path to a JSON sidecar declaring components.securitySchemes + per-op overrides")
	return cmd
}

func (a *App) newLintCmd() *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:     "lint <input.apib | ->",
		Aliases: []string{"check"},
		Short:   "Parse an API Blueprint document and report Drafter diagnostics (no conversion)",
		Long: `Lint runs the input through the embedded Drafter parser and prints every
diagnostic (note / warning / error) to stderr in the same form as
` + "`convert`" + `. It performs no OAS conversion, which makes it useful in pre-commit
hooks or CI gates where you want to fail the build on parse problems
without spending time emitting YAML.`,
		Example: `  apib-to-oas lint spec.apib
  apib-to-oas lint spec.apib --strict     # also fails on warnings
  cat spec.apib | apib-to-oas lint -`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLint(cmd.Context(), args[0], strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero if Drafter emits any warning (errors always fail)")
	return cmd
}

func (a *App) runConvert(ctx context.Context, in, out, format, oasVersion, infoVersion string, strict, quiet bool, securityConfig string) error {
	src, srcLabel, err := a.readInput(in)
	if err != nil {
		return err
	}

	secCfg, err := convert.LoadSecurityConfig(securityConfig)
	if err != nil {
		return err
	}

	runner, err := drafter.New()
	if err != nil {
		return fmt.Errorf("init drafter: %w", err)
	}

	ast, err := runner.Parse(ctx, src)
	if err != nil {
		return parseErr(fmt.Errorf("parse %s: %w", srcLabel, err))
	}

	annotations := convert.ExtractAnnotations(ast)
	a.printAnnotations(annotations, srcLabel, quiet)
	if convert.HasErrors(annotations) {
		return parseErr(fmt.Errorf("apib parse reported errors (see above)"))
	}
	if strict && len(annotations) > 0 {
		return parseErr(fmt.Errorf("apib parse reported %d warning(s) (--strict)", len(annotations)))
	}

	diagBuf := convert.NewDiagnostics()
	doc, err := convert.RefractToOASWithOptions(ast, convert.Options{
		OASVersion:  oasVersion,
		InfoVersion: infoVersion,
		Security:    secCfg,
		Diagnostics: diagBuf,
	})
	if err != nil {
		return convertErr(fmt.Errorf("convert to oas: %w", err))
	}
	// Surface Blueprint+ converter diagnostics next to drafter's.
	a.printAnnotations(diagBuf.Items, srcLabel, quiet)
	if diagBuf.HasErrors() {
		return convertErr(fmt.Errorf("blueprint+ conversion reported errors (see above)"))
	}
	if strict && len(diagBuf.Items) > 0 {
		return parseErr(fmt.Errorf("blueprint+ conversion reported %d warning(s) (--strict)", len(diagBuf.Items)))
	}

	if format == "" {
		format = inferFormat(out)
	}
	data, err := convert.Marshal(doc, format)
	if err != nil {
		return convertErr(fmt.Errorf("marshal %s: %w", format, err))
	}

	if out == "-" {
		_, err = a.Stdout.Write(data)
		return err
	}
	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}
	return os.WriteFile(out, data, 0o644)
}

func (a *App) runLint(ctx context.Context, in string, strict bool) error {
	src, srcLabel, err := a.readInput(in)
	if err != nil {
		return err
	}
	runner, err := drafter.New()
	if err != nil {
		return fmt.Errorf("init drafter: %w", err)
	}
	ast, err := runner.Parse(ctx, src)
	if err != nil {
		return parseErr(fmt.Errorf("parse %s: %w", srcLabel, err))
	}
	annotations := convert.ExtractAnnotations(ast)
	a.printAnnotations(annotations, srcLabel, false)
	if convert.HasErrors(annotations) {
		return parseErr(fmt.Errorf("%s: parse errors", srcLabel))
	}
	if strict && len(annotations) > 0 {
		return parseErr(fmt.Errorf("%s: %d warning(s) (--strict)", srcLabel, len(annotations)))
	}
	if len(annotations) == 0 {
		fmt.Fprintf(a.Stderr, "%s: ok\n", srcLabel)
	}
	return nil
}

// readInput returns the source bytes plus a human label for diagnostics.
// `-` reads from a.Stdin and labels as "<stdin>".
func (a *App) readInput(path string) (data []byte, label string, err error) {
	if path == "-" {
		b, err := io.ReadAll(a.Stdin)
		if err != nil {
			return nil, "<stdin>", fmt.Errorf("read stdin: %w", err)
		}
		return b, "<stdin>", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("read input: %w", err)
	}
	return b, path, nil
}

// inferFormat picks an output format from the output path's extension.
// Falls back to "yaml" for stdout or unknown extensions.
func inferFormat(out string) string {
	switch strings.ToLower(filepath.Ext(out)) {
	case ".json":
		return "json"
	case ".yaml", ".yml", "":
		return "yaml"
	default:
		return "yaml"
	}
}

// printAnnotations writes diagnostics to stderr in the
// `path:line:col: severity [code]: msg` shape that most editors will
// auto-link. With quiet=true only error-severity entries are printed;
// errors always print. Blueprint+ codes (E003, W002, ...) are
// preferred over Drafter's numeric codes when available.
func (a *App) printAnnotations(anns []convert.Annotation, srcLabel string, quiet bool) {
	for _, an := range anns {
		if quiet && an.Severity != "error" {
			continue
		}
		sev := colourSeverity(an.Severity, isTerminal(a.Stderr))
		var codeBracket string
		switch {
		case an.StableCode != "":
			codeBracket = "[" + an.StableCode + "]"
		case an.Code != 0:
			codeBracket = fmt.Sprintf("[code %d]", an.Code)
		default:
			codeBracket = "[code -]"
		}
		fmt.Fprintf(a.Stderr, "%s:%d:%d: %s %s: %s\n",
			srcLabel, an.Line, an.Column, sev, codeBracket, an.Message)
	}
}

// colourSeverity wraps the severity word in ANSI colour when the
// stream is a TTY and --no-colour is not set. Returns the bare word
// otherwise (CI logs, file redirects, NO_COLOUR=1).
func colourSeverity(sev string, tty bool) string {
	if !tty || noColor {
		return sev
	}
	switch sev {
	case "error":
		return "\x1b[31;1m" + sev + "\x1b[0m" // bold red
	case "warning":
		return "\x1b[33;1m" + sev + "\x1b[0m" // bold yellow
	case "note":
		return "\x1b[36m" + sev + "\x1b[0m" // cyan
	default:
		return sev
	}
}

// isTerminal reports whether w is an *os.File pointing at a TTY.
// Done by hand to avoid pulling in golang.org/x/term (cobra is the
// only third-party dep).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

// exitCodeError carries a process exit code through the error chain
// so cmd/apib-to-oas/main.go can pass it to os.Exit. ExitCode unwraps it.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

// ExitCode extracts the exit code attached to err. Returns ExitGeneric
// for plain errors, 0 for nil.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	e := &exitCodeError{}
	if errors.As(err, &e) {
		return e.code
	}
	return ExitGeneric
}

func parseErr(err error) error   { return &exitCodeError{code: ExitParseError, err: err} }
func convertErr(err error) error { return &exitCodeError{code: ExitConvertErr, err: err} }
