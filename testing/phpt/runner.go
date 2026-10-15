package phpt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Options selects standalone validation or differential validation when Oracle
// is nonempty. Timeout bounds each process; zero selects ten seconds.
type Options struct {
	Target, Oracle string
	Timeout        time.Duration
}

type Output struct {
	Stdout, Stderr []byte
	ExitCode       int
}

type Result struct {
	Name, Verdict, Reason string
	Target                Output
	Oracle                *Output
}

// Run refreshes local fixtures for each interpreter inside the same private
// directory, so path diagnostics remain comparable without normalization.
// This is process/work-directory isolation, not a sandbox for untrusted PHP.
// Both modes require empty stderr and exit zero. Differential mode first compares
// stdout, stderr and exit code independently against the oracle.
func Run(ctx context.Context, path string, options Options) (result Result, err error) {
	if options.Timeout < 0 || options.Target == "" {
		return result, fmt.Errorf("a target and a nonnegative timeout are required")
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return result, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	test, err := Parse(data)
	if err != nil {
		return result, err
	}
	result.Name = test.Name
	if test.ExpectKind != "EXPECT" {
		if _, err := matchWithOracle(options.Oracle, test.ExpectKind, test.Expected, nil); err != nil {
			return result, err
		}
	}
	source := filepath.Dir(path)
	program := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".php"
	if test.External != "" {
		if !filepath.IsLocal(test.External) {
			return result, fmt.Errorf("FILE_EXTERNAL must stay inside the test directory: %q", test.External)
		}
		program = test.External
	}
	workspace, err := os.MkdirTemp("", "phpt-")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(workspace)) }()
	if relative, relErr := filepath.Rel(source, workspace); relErr != nil || filepath.IsLocal(relative) {
		return result, fmt.Errorf("temporary directory must be outside the fixture tree; set TMPDIR accordingly")
	}
	fixtures, err := os.OpenRoot(source)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, fixtures.Close()) }()
	directories := []string{"."}
	if test.External != "" && filepath.Dir(program) != "." {
		for parent := filepath.Dir(program); parent != "."; parent = filepath.Dir(parent) {
			info, statErr := fixtures.Lstat(parent)
			if statErr != nil {
				return result, statErr
			}
			if !info.IsDir() {
				return result, fmt.Errorf("unsupported fixture directory: %s", parent)
			}
		}
		directories = append(directories, filepath.Dir(program))
	}
	var names []string
	for _, directory := range directories {
		entries, readErr := fs.ReadDir(fixtures.FS(), directory)
		if readErr != nil {
			return result, readErr
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := filepath.Join(directory, entry.Name())
			if !entry.Type().IsRegular() {
				return result, fmt.Errorf("unsupported fixture type: %s", name)
			}
			if filepath.Ext(name) != ".phpt" || name == test.External {
				names = append(names, name)
			}
		}
	}
	run := func(binary string) (output Output, skipped bool, runErr error) {
		binary, runErr = exec.LookPath(binary)
		if runErr != nil {
			return
		}
		binary, runErr = filepath.Abs(binary)
		if runErr != nil {
			return
		}
		if runErr = os.Chmod(workspace, 0o700); runErr != nil {
			return
		}
		entries, readErr := os.ReadDir(workspace)
		if readErr != nil {
			runErr = readErr
			return
		}
		for _, entry := range entries {
			if runErr = os.RemoveAll(filepath.Join(workspace, entry.Name())); runErr != nil {
				return
			}
		}
		for _, name := range names {
			if runErr = os.MkdirAll(filepath.Dir(filepath.Join(workspace, name)), 0o700); runErr != nil {
				return
			}
			if runErr = copyFixture(fixtures, name, filepath.Join(workspace, name)); runErr != nil {
				return
			}
		}
		if test.External == "" {
			if runErr = writeProgram(filepath.Join(workspace, program), test.Code); runErr != nil {
				return
			}
		} else {
			var info fs.FileInfo
			info, runErr = os.Stat(filepath.Join(workspace, program))
			if runErr != nil {
				return
			}
			if !info.Mode().IsRegular() {
				runErr = fmt.Errorf("FILE_EXTERNAL is not a regular file")
				return
			}
		}
		if test.HasSkipIf {
			skipPath := filepath.Join(workspace, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".skip.php")
			if runErr = writeProgram(skipPath, test.SkipIf); runErr != nil {
				return
			}
			output, runErr = execute(ctx, binary, skipPath, workspace, options.Timeout)
			if runErr != nil {
				return
			}
			if output.ExitCode != 0 || len(output.Stderr) != 0 {
				runErr = fmt.Errorf("invalid SKIPIF: exit=%d stderr=%q", output.ExitCode, output.Stderr)
				return
			}
			if bytes.HasPrefix(output.Stdout, []byte("skip")) {
				skipped = true
				return
			}
			if len(output.Stdout) != 0 {
				runErr = fmt.Errorf("unexpected SKIPIF output: %q", output.Stdout)
				return
			}
		}
		output, runErr = execute(ctx, binary, filepath.Join(workspace, program), workspace, options.Timeout)
		return
	}
	var oracleSkipped bool
	if options.Oracle != "" {
		var reference Output
		reference, oracleSkipped, err = run(options.Oracle)
		if err != nil {
			return result, fmt.Errorf("oracle: %w", err)
		}
		result.Oracle = &reference
	}
	var targetSkipped bool
	result.Target, targetSkipped, err = run(options.Target)
	if err != nil {
		return result, fmt.Errorf("target: %w", err)
	}
	result.Verdict = "FAIL"
	if result.Oracle != nil {
		if oracleSkipped != targetSkipped {
			result.Reason = "SKIPIF decisions differ"
			return result, nil
		}
		var differences []string
		if !bytes.Equal(result.Oracle.Stdout, result.Target.Stdout) {
			differences = append(differences, "stdout")
		}
		if !bytes.Equal(result.Oracle.Stderr, result.Target.Stderr) {
			differences = append(differences, "stderr")
		}
		if result.Oracle.ExitCode != result.Target.ExitCode {
			differences = append(differences, "exit code")
		}
		if len(differences) != 0 {
			result.Reason = "oracle divergence: " + strings.Join(differences, ", ")
			return result, nil
		}
	}
	if targetSkipped {
		result.Verdict, result.Reason = "SKIP", string(result.Target.Stdout)
		return result, nil
	}
	actual := result.Target.Stdout
	if len(actual) == 0 && len(result.Target.Stderr) != 0 {
		actual = result.Target.Stderr
	}
	matched, err := matchWithOracle(options.Oracle, test.ExpectKind, test.Expected, actual)
	if err != nil {
		return result, err
	}
	if !matched {
		result.Reason = test.ExpectKind + " does not match stdout"
		return result, nil
	}
	if len(result.Target.Stdout) != 0 && len(result.Target.Stderr) != 0 {
		result.Reason = "PHPT expectation requires empty stderr when stdout is nonempty"
		return result, nil
	}
	if len(test.Expected) == 0 && (result.Target.ExitCode != 0 || len(result.Target.Stderr) != 0) {
		result.Reason = "PHPT expectation requires exit zero and empty stderr"
		return result, nil
	}
	result.Verdict = "PASS"
	return result, nil
}

func copyFixture(source *os.Root, name, destination string) (err error) {
	input, err := source.Open(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported fixture type: %s", name)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600|info.Mode().Perm()&0o111)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	_, err = io.Copy(output, input)
	return err
}

func writeProgram(path string, code []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, err = file.Write(code)
	return err
}

type outputBuffer struct {
	data     bytes.Buffer
	overflow bool
}

func (buffer *outputBuffer) Write(data []byte) (int, error) {
	const limit = 8 << 20
	if len(data) > limit-buffer.data.Len() {
		buffer.overflow = true
		return 0, fmt.Errorf("process output exceeds %d bytes", limit)
	}
	return buffer.data.Write(data)
}

func execute(parent context.Context, binary, program, dir string, timeout time.Duration) (Output, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-n", program)
	command.Dir = dir
	command.Env = append(os.Environ(), "TMPDIR="+dir, "HOME="+dir)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = time.Second
	var stdout, stderr outputBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		return Output{}, err
	}
	defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	err := command.Wait()
	output := Output{Stdout: bytes.Clone(stdout.data.Bytes()), Stderr: bytes.Clone(stderr.data.Bytes()), ExitCode: command.ProcessState.ExitCode()}
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	if stdout.overflow || stderr.overflow {
		return output, fmt.Errorf("process output limit exceeded")
	}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() < 0 {
			return output, fmt.Errorf("process execution: %w", err)
		}
	}
	return output, nil
}
