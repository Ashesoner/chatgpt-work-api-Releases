package commandproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/processlaunch"
)

const (
	Argument           = "--cwapi-command-proxy"
	Schema             = "cwapi.command-proxy.v1"
	segmentBytes int64 = 256 * 1024
	maxSegments        = 16
)

type Payload struct {
	Schema       string   `json:"schema"`
	Executable   string   `json:"executable"`
	Argv         []string `json:"argv,omitempty"`
	CWD          string   `json:"cwd"`
	StdoutDir    string   `json:"stdout_dir"`
	StderrDir    string   `json:"stderr_dir"`
	MirrorOutput bool     `json:"mirror_output,omitempty"`
}

type ReadResult struct {
	Data      string
	Cursor    int64
	Truncated bool
}

func IsInvocation(args []string) (string, bool) {
	if len(args) != 3 || args[1] != Argument {
		return "", false
	}
	path := strings.TrimSpace(args[2])
	return path, path != ""
}

func WritePayload(path string, payload Payload) error {
	payload.Schema = Schema
	if err := validatePayload(payload); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("COMMAND_PROXY_PAYLOAD_ENCODE_FAILED: %w", err)
	}
	if len(encoded) > 256*1024 {
		return errors.New("COMMAND_PROXY_PAYLOAD_TOO_LARGE")
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("COMMAND_PROXY_PAYLOAD_WRITE_FAILED: %w", err)
	}
	return nil
}

func Run(path string) int {
	payload, err := loadPayload(path)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		return 126
	}
	stdoutLog, err := newSegmentedWriter(payload.StdoutDir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		return 126
	}
	defer stdoutLog.Close()
	stderrLog, err := newSegmentedWriter(payload.StderrDir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		return 126
	}
	defer stderrLog.Close()

	command := processlaunch.Command(payload.Executable, payload.Argv...)
	command.Dir = payload.CWD
	command.Env = os.Environ()
	if payload.MirrorOutput {
		command.Stdout = io.MultiWriter(os.Stdout, stdoutLog)
		command.Stderr = io.MultiWriter(os.Stderr, stderrLog)
	} else {
		command.Stdout = stdoutLog
		command.Stderr = stderrLog
	}
	if err := command.Run(); err != nil {
		if command.ProcessState != nil {
			return command.ProcessState.ExitCode()
		}
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		return 126
	}
	if command.ProcessState == nil {
		return 126
	}
	return command.ProcessState.ExitCode()
}

func loadPayload(path string) (Payload, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || !filepath.IsAbs(path) {
		return Payload{}, errors.New("COMMAND_PROXY_PAYLOAD_PATH_INVALID")
	}
	file, err := os.Open(path)
	if err != nil {
		return Payload{}, fmt.Errorf("COMMAND_PROXY_PAYLOAD_OPEN_FAILED: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 256*1024+1))
	decoder.DisallowUnknownFields()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("COMMAND_PROXY_PAYLOAD_INVALID: %w", err)
	}
	if err := validatePayload(payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func validatePayload(payload Payload) error {
	if payload.Schema != Schema {
		return errors.New("COMMAND_PROXY_SCHEMA_INVALID")
	}
	if !filepath.IsAbs(payload.Executable) || !filepath.IsAbs(payload.CWD) || !filepath.IsAbs(payload.StdoutDir) || !filepath.IsAbs(payload.StderrDir) {
		return errors.New("COMMAND_PROXY_PATH_INVALID")
	}
	if len(payload.Argv) > 4096 {
		return errors.New("COMMAND_PROXY_ARGV_TOO_LARGE")
	}
	for _, value := range payload.Argv {
		if strings.IndexByte(value, 0) >= 0 {
			return errors.New("COMMAND_PROXY_ARGV_INVALID")
		}
	}
	return nil
}

type segmentedWriter struct {
	dir     string
	current *os.File
	start   int64
	size    int64
	starts  []int64
}

func newSegmentedWriter(dir string) (*segmentedWriter, error) {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return nil, errors.New("COMMAND_PROXY_LOG_DIR_INVALID")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("COMMAND_PROXY_LOG_DIR_CREATE_FAILED: %w", err)
	}
	writer := &segmentedWriter{dir: dir}
	if err := writer.rotate(0); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *segmentedWriter) Write(payload []byte) (int, error) {
	total := 0
	for len(payload) > 0 {
		remaining := segmentBytes - w.size
		if remaining <= 0 {
			if err := w.rotate(w.start + w.size); err != nil {
				return total, err
			}
			remaining = segmentBytes
		}
		count := len(payload)
		if int64(count) > remaining {
			count = int(remaining)
		}
		n, err := w.current.Write(payload[:count])
		total += n
		w.size += int64(n)
		payload = payload[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (w *segmentedWriter) rotate(start int64) error {
	if w.current != nil {
		_ = w.current.Close()
	}
	path := filepath.Join(w.dir, segmentName(start))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("COMMAND_PROXY_LOG_OPEN_FAILED: %w", err)
	}
	w.current, w.start, w.size = file, start, 0
	w.starts = append(w.starts, start)
	for len(w.starts) > maxSegments {
		oldest := w.starts[0]
		w.starts = w.starts[1:]
		_ = os.Remove(filepath.Join(w.dir, segmentName(oldest)))
	}
	return nil
}

func (w *segmentedWriter) Close() error {
	if w == nil || w.current == nil {
		return nil
	}
	return w.current.Close()
}

func segmentName(start int64) string { return fmt.Sprintf("%020d.log", start) }

func ReadSince(dir string, cursor int64, limit int) (ReadResult, error) {
	if cursor < 0 {
		return ReadResult{}, errors.New("COMMAND_PROXY_CURSOR_INVALID")
	}
	if limit <= 0 {
		limit = 64 * 1024
	}
	segments, err := listSegments(dir)
	if err != nil {
		return ReadResult{}, err
	}
	if len(segments) == 0 {
		return ReadResult{Cursor: cursor}, nil
	}
	earliest := segments[0].start
	truncated := cursor < earliest
	if truncated {
		cursor = earliest
	}
	result := ReadResult{Cursor: cursor, Truncated: truncated}
	var builder strings.Builder
	for _, segment := range segments {
		info, statErr := os.Stat(segment.path)
		if statErr != nil {
			continue
		}
		end := segment.start + info.Size()
		if cursor >= end {
			continue
		}
		offset := int64(0)
		if cursor > segment.start {
			offset = cursor - segment.start
		}
		file, openErr := os.Open(segment.path)
		if openErr != nil {
			continue
		}
		if _, seekErr := file.Seek(offset, io.SeekStart); seekErr != nil {
			_ = file.Close()
			continue
		}
		remaining := limit - builder.Len()
		if remaining <= 0 {
			_ = file.Close()
			break
		}
		chunk, readErr := io.ReadAll(io.LimitReader(file, int64(remaining)))
		_ = file.Close()
		if readErr != nil {
			return ReadResult{}, fmt.Errorf("COMMAND_PROXY_LOG_READ_FAILED: %w", readErr)
		}
		builder.Write(chunk)
		result.Cursor = segment.start + offset + int64(len(chunk))
		if builder.Len() >= limit {
			break
		}
	}
	result.Data = builder.String()
	return result, nil
}

func ReadTail(dir string, limit int) (ReadResult, error) {
	if limit <= 0 {
		limit = 512 * 1024
	}
	segments, err := listSegments(dir)
	if err != nil {
		return ReadResult{}, err
	}
	if len(segments) == 0 {
		return ReadResult{}, nil
	}
	end := int64(0)
	for _, segment := range segments {
		if info, statErr := os.Stat(segment.path); statErr == nil && segment.start+info.Size() > end {
			end = segment.start + info.Size()
		}
	}
	cursor := end - int64(limit)
	if cursor < 0 {
		cursor = 0
	}
	result, err := ReadSince(dir, cursor, limit)
	if err != nil {
		return ReadResult{}, err
	}
	result.Cursor = end
	if cursor > 0 {
		result.Truncated = true
	}
	return result, nil
}

type segmentInfo struct {
	start int64
	path  string
}

func listSegments(dir string) ([]segmentInfo, error) {
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("COMMAND_PROXY_LOG_LIST_FAILED: %w", err)
	}
	result := make([]segmentInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		value := strings.TrimSuffix(entry.Name(), ".log")
		start, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || start < 0 {
			continue
		}
		result = append(result, segmentInfo{start: start, path: filepath.Join(dir, entry.Name())})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].start < result[j].start })
	return result, nil
}
