// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Implémentation native monolog/monolog et norme PSR-3

package builtins

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelNotice
	LevelWarning
	LevelError
	LevelCritical
	LevelAlert
	LevelEmergency
)

var levelNames = map[LogLevel]string{
	LevelDebug:     "DEBUG",
	LevelInfo:      "INFO",
	LevelNotice:    "NOTICE",
	LevelWarning:   "WARNING",
	LevelError:     "ERROR",
	LevelCritical:  "CRITICAL",
	LevelAlert:     "ALERT",
	LevelEmergency: "EMERGENCY",
}

type Logger struct {
	mu       sync.Mutex
	name     string
	minLevel LogLevel
	writer   io.Writer
}

func NewLogger(name string, minLevel LogLevel, out io.Writer) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{
		name:     name,
		minLevel: minLevel,
		writer:   out,
	}
}

func (l *Logger) Log(lvl LogLevel, message string, context map[string]string) string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lvl < l.minLevel {
		return ""
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	lvlName := levelNames[lvl]

	ctxStr := ""
	if len(context) > 0 {
		var pairs []string
		for k, v := range context {
			pairs = append(pairs, fmt.Sprintf("%q:%q", k, v))
		}
		ctxStr = "{" + strings.Join(pairs, ",") + "}"
	} else {
		ctxStr = "[]"
	}

	line := fmt.Sprintf("[%s] %s.%s: %s %s []\n", ts, l.name, lvlName, message, ctxStr)
	_, _ = l.writer.Write([]byte(line))
	return strings.TrimSpace(line)
}

func LogMessage(levelVal, msgVal, nameVal types.Value) types.Value {
	lvlStr := strings.ToUpper(levelVal.String())
	lvl := LevelInfo
	switch lvlStr {
	case "DEBUG":
		lvl = LevelDebug
	case "INFO":
		lvl = LevelInfo
	case "NOTICE":
		lvl = LevelNotice
	case "WARNING", "WARN":
		lvl = LevelWarning
	case "ERROR":
		lvl = LevelError
	case "CRITICAL":
		lvl = LevelCritical
	case "ALERT":
		lvl = LevelAlert
	case "EMERGENCY":
		lvl = LevelEmergency
	}

	name := "app"
	if nameVal.Type == types.TypeString && nameVal.String() != "" {
		name = nameVal.String()
	}

	l := NewLogger(name, LevelDebug, os.Stdout)
	entry := l.Log(lvl, msgVal.String(), nil)
	return types.NewString(entry)
}
