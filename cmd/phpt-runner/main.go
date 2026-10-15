// Command phpt-runner validates PHPT files, optionally against a second binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"code.hazyhaar.fr/devhoros/php127/testing/phpt"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("phpt-runner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	target := flags.String("target", "/usr/bin/php", "PHP binary to validate")
	oracle := flags.String("oracle", "", "reference PHP binary for strict differential comparison")
	timeout := flags.Duration("timeout", 10*time.Second, "timeout per PHP process")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: phpt-runner [--oracle /usr/bin/php] [--target binary] [--timeout 10s] <file.phpt or directory>...")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "Exit codes: 0 = no failures, 1 = test failure, 2 = input/execution error. SKIP is counted separately.")
		fmt.Fprintln(stderr, "Outputs are never trimmed. Regex syntax is PCRE2 via the PHP oracle; compilation and evaluation failures are errors.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() == 0 || *timeout <= 0 || *target == "" {
		flags.Usage()
		return 2
	}
	selected := make(map[string]bool)
	for _, input := range flags.Args() {
		path, err := filepath.Abs(input)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		info, err := os.Lstat(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if !info.IsDir() && (!info.Mode().IsRegular() || filepath.Ext(path) != ".phpt") {
			fmt.Fprintf(stderr, "Not a regular .phpt file or directory: %s\n", path)
			return 2
		}
		err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in test selection: %s", name)
			}
			if !entry.IsDir() && filepath.Ext(name) == ".phpt" {
				if !entry.Type().IsRegular() {
					return fmt.Errorf("not a regular PHPT file: %s", name)
				}
				selected[name] = true
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if len(selected) == 0 {
		fmt.Fprintln(stderr, "No .phpt tests found.")
		return 2
	}
	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	counts := make(map[string]int)
	code := 0
	total := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(stderr, err)
			code = 2
			break
		}
		total++
		result, err := phpt.Run(ctx, path, phpt.Options{Target: *target, Oracle: *oracle, Timeout: *timeout})
		if err != nil {
			counts["ERROR"]++
			code = 2
			fmt.Fprintf(stdout, "ERROR %s: %v\n", path, err)
			continue
		}
		counts[result.Verdict]++
		fmt.Fprintf(stdout, "%s %s: %q", result.Verdict, path, result.Name)
		if result.Reason != "" {
			fmt.Fprintf(stdout, " (%q)", result.Reason)
		}
		fmt.Fprintln(stdout)
		if result.Verdict == "FAIL" {
			if code == 0 {
				code = 1
			}
			printOutput(stdout, "target", result.Target)
			if result.Oracle != nil {
				printOutput(stdout, "oracle", *result.Oracle)
			}
		}
	}
	if ctx.Err() != nil {
		code = 2
	}
	fmt.Fprintf(stdout, "TOTAL=%d PASS=%d FAIL=%d SKIP=%d ERROR=%d\n", total, counts["PASS"], counts["FAIL"], counts["SKIP"], counts["ERROR"])
	return code
}

func printOutput(writer io.Writer, label string, output phpt.Output) {
	fmt.Fprintf(writer, "  %s exit=%d\n", label, output.ExitCode)
	for _, stream := range []struct {
		name string
		data []byte
	}{{"stdout", output.Stdout}, {"stderr", output.Stderr}} {
		shown := stream.data[:min(len(stream.data), 4096)]
		fmt.Fprintf(writer, "  %s %s (%d bytes, first %d shown)=%q\n", label, stream.name, len(stream.data), len(shown), shown)
	}
}
