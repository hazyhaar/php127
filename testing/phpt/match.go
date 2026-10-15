package phpt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Match compares EXPECT in Go and delegates regex semantics to the PHP oracle.
// Neither expectations nor output undergo whitespace or newline normalization.
func Match(kind string, expected, actual []byte) (bool, error) {
	return matchWithOracle("/usr/bin/php", kind, expected, actual)
}

var matcher phpMatcher

type phpMatcher struct {
	mu            sync.Mutex
	path          string
	cmd           *exec.Cmd
	input, output *os.File
}

func matchWithOracle(oracle, kind string, expected, actual []byte) (bool, error) {
	if kind == "EXPECT" {
		return bytes.Equal(expected, actual), nil
	}
	if kind != "EXPECTF" && kind != "EXPECTREGEX" {
		return false, fmt.Errorf("unsupported expectation kind %q", kind)
	}
	if kind == "EXPECTF" && bytes.Count(expected, []byte("%r"))%2 != 0 {
		return false, fmt.Errorf("unterminated EXPECTF %%r block")
	}
	if uint64(len(expected)) > math.MaxUint32 || uint64(len(actual)) > math.MaxUint32 {
		return false, fmt.Errorf("PHP matcher frame exceeds uint32 length")
	}
	if oracle == "" {
		oracle = "/usr/bin/php"
	}
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	if matcher.path != oracle {
		matcher.stop()
	}
	var err error
	// Only transport failures restart the worker; PCRE errors remain errors.
	for range 2 {
		if matcher.cmd == nil {
			if err = matcher.start(oracle); err != nil {
				return false, fmt.Errorf("PHP matcher start: %w", err)
			}
		}
		var reply []byte
		reply, err = matcher.exchange(kind, expected, actual)
		if err == nil {
			switch string(reply) {
			case "0":
				return false, nil
			case "1":
				return true, nil
			}
			if len(reply) > 1 && reply[0] == 'E' {
				return false, fmt.Errorf("%s PCRE: %s", kind, reply[1:])
			}
			err = fmt.Errorf("invalid PHP matcher response %q", reply)
		}
		matcher.stop()
	}
	return false, fmt.Errorf("PHP matcher transport: %w", err)
}

// stop is called with mu held. Kill and Wait also reap an already dead worker.
func (worker *phpMatcher) stop() {
	if worker.cmd != nil {
		_ = worker.cmd.Process.Kill()
		_ = worker.input.Close()
		_ = worker.output.Close()
		_ = worker.cmd.Wait()
		worker.cmd = nil
	}
}

func (worker *phpMatcher) start(path string) error {
	childInput, input, err := os.Pipe()
	if err != nil {
		return err
	}
	defer childInput.Close()
	output, childOutput, err := os.Pipe()
	if err != nil {
		input.Close()
		return err
	}
	defer childOutput.Close()
	command := exec.Command(path, "-n", "-r", phpMatchWorker)
	command.Stdin, command.Stdout = childInput, childOutput
	// The runner already targets Linux. Do not leave a worker on parent death.
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	worker.path, worker.cmd, worker.input, worker.output = path, command, input, output
	return nil
}

func (worker *phpMatcher) exchange(kind string, expected, actual []byte) ([]byte, error) {
	deadline := time.Now().Add(10 * time.Second)
	if err := worker.input.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err := worker.output.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	var header [12]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(kind)))
	binary.BigEndian.PutUint32(header[4:8], uint32(len(expected)))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(actual)))
	for _, part := range [][]byte{header[:], []byte(kind), expected, actual} {
		if _, err := worker.input.Write(part); err != nil {
			return nil, err
		}
	}
	if _, err := io.ReadFull(worker.output, header[:4]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length == 0 || length > 65536 {
		return nil, fmt.Errorf("invalid PHP matcher response length %d", length)
	}
	reply := make([]byte, length)
	_, err := io.ReadFull(worker.output, reply)
	return reply, err
}

// EXPECTF follows php-8.3.6/run-tests.php, except CRLF normalization and the
// explicitly rejected unclosed raw block. Capturing wrappers preserve numbering.
const phpMatchWorker = `
function read_exact($length) {
    $data = '';
    while (strlen($data) < $length) {
        $part = fread(STDIN, $length - strlen($data));
        if ($part === false || $part === '') { exit(0); }
        $data .= $part;
    }
    return $data;
}
set_error_handler(function($severity, $message) { throw new ErrorException($message, 0, $severity); });
while (true) {
    $sizes = unpack('Nkind/Nexpected/Nactual', read_exact(12));
    $kind = read_exact($sizes['kind']);
    $wanted = read_exact($sizes['expected']);
    $actual = read_exact($sizes['actual']);
    try {
        $re = $wanted;
        if ($kind === 'EXPECTF') {
            $re = ''; $offset = 0; $length = strlen($wanted);
            while ($offset < $length) {
                $start = strpos($wanted, '%r', $offset);
                if ($start === false) {
                    $re .= preg_quote(substr($wanted, $offset), '/');
                    break;
                }
                $end = strpos($wanted, '%r', $start + 2);
                if ($end === false) { throw new Exception('unterminated EXPECTF %r block'); }
                $re .= preg_quote(substr($wanted, $offset, $start - $offset), '/');
                $re .= '(' . substr($wanted, $start + 2, $end - $start - 2) . ')';
                $offset = $end + 2;
            }
            $re = strtr($re, [
                '%e' => preg_quote(DIRECTORY_SEPARATOR, '/'),
                '%s' => '[^\r\n]+', '%S' => '[^\r\n]*',
                '%a' => '.+', '%A' => '.*', '%w' => '\s*',
                '%i' => '[+-]?\d+', '%d' => '\d+', '%x' => '[0-9a-fA-F]+',
                '%f' => '[+-]?(?:\d+|(?=\.\d))(?:\.\d+)?(?:[Ee][+-]?\d+)?',
                '%c' => '.', '%0' => '\x00',
            ]);
        }
        $official = '/^' . $re . '$/s';
        $strict = '/\A(?:' . $re . ')\z/s';
        if (preg_match($official, '') === false || preg_match($strict, '') === false) {
            throw new Exception(preg_last_error_msg());
        }
        $r1 = preg_match($official, $actual);
        $error1 = $r1 === false ? preg_last_error_msg() : null;
        $r2 = preg_match($strict, $actual);
        if ($r1 === false || $r2 === false) {
            throw new Exception($error1 ?? preg_last_error_msg());
        }
        $reply = $r1 === 1 && $r2 === 1 ? '1' : '0';
    } catch (Throwable $error) {
        $reply = 'E' . $error->getMessage();
    }
    $frame = pack('N', strlen($reply)) . $reply;
    while ($frame !== '') {
        $written = fwrite(STDOUT, $frame);
        if ($written === false || $written === 0) { exit(1); }
        $frame = substr($frame, $written);
    }
    fflush(STDOUT);
}`
