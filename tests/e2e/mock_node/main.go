package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// safeMarkerPath constrains marker files to the system temp dir so paths from the environment
// cannot point at arbitrary filesystem locations (test helper, not production).
func safeMarkerPath(envKey, defaultName string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(envKey))
	var candidate string
	switch {
	case raw == "":
		candidate = filepath.Join(os.TempDir(), defaultName)
	case filepath.IsAbs(raw):
		candidate = filepath.Clean(raw)
	default:
		candidate = filepath.Join(os.TempDir(), filepath.Clean(raw))
	}

	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("marker path: %w", err)
	}
	tmpRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	rel, err := filepath.Rel(tmpRoot, abs)
	if err != nil {
		return "", fmt.Errorf("marker path must be under temp directory")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("marker path escapes temp directory")
	}
	return abs, nil
}

func main() {
	fmt.Fprintf(os.Stdout, "MOCK_NODE_ARGS=%s\n", strings.Join(os.Args[1:], " "))

	mode := os.Getenv("MOCK_NODE_MODE")
	switch mode {
	case "exit0":
		os.Exit(0)
	case "exit1":
		os.Exit(1)
	case "exit_code":
		code, _ := strconv.Atoi(os.Getenv("MOCK_NODE_EXIT_CODE"))
		os.Exit(code)
	case "signal":
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		fmt.Fprintln(os.Stdout, "MOCK_NODE_SIGNAL_RECEIVED")
		os.Exit(0)
	case "crash_then_ok":
		marker, err := safeMarkerPath("MOCK_NODE_MARKER", "mock_node_marker")
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			_ = os.WriteFile(marker, []byte("1"), 0o600)
			fmt.Fprintln(os.Stdout, "MOCK_NODE_CRASH")
			os.Exit(1)
		}
		_ = os.Remove(marker)
		fmt.Fprintln(os.Stdout, "MOCK_NODE_RECOVERED")
		os.Exit(0)
	case "slow":
		time.Sleep(60 * time.Second)
	case "run_for":
		duration := parseDurationEnv("MOCK_NODE_DURATION", 2*time.Second)
		interval := parseDurationEnv("MOCK_NODE_INTERVAL", 200*time.Millisecond)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

		ticker := time.NewTicker(interval)
		deadline := time.After(duration)
		block := 1

		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(os.Stdout, "MOCK_NODE_BLOCK=%d\n", block)
				fmt.Fprintf(os.Stderr, "MOCK_NODE_LOG block=%d imported\n", block)
				block++
			case sig := <-sigCh:
				ticker.Stop()
				fmt.Fprintf(os.Stdout, "MOCK_NODE_SIGNAL_RECEIVED=%s\n", sig)
				fmt.Fprintf(os.Stdout, "MOCK_NODE_BLOCKS_PRODUCED=%d\n", block-1)
				os.Exit(0)
			case <-deadline:
				ticker.Stop()
				fmt.Fprintf(os.Stdout, "MOCK_NODE_BLOCKS_PRODUCED=%d\n", block-1)
				fmt.Fprintln(os.Stdout, "MOCK_NODE_CLEAN_EXIT")
				os.Exit(0)
			}
		}
	case "crash_after":
		duration := parseDurationEnv("MOCK_NODE_DURATION", 1*time.Second)
		fmt.Fprintln(os.Stdout, "MOCK_NODE_STARTED")
		time.Sleep(duration)
		fmt.Fprintln(os.Stdout, "MOCK_NODE_CRASH_AFTER_RUN")
		os.Exit(1)
	case "crash_after_then_ok":
		duration := parseDurationEnv("MOCK_NODE_DURATION", 500*time.Millisecond)
		marker, err := safeMarkerPath("MOCK_NODE_MARKER", "mock_node_crash_after_marker")
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			_ = os.WriteFile(marker, []byte("1"), 0o600)
			fmt.Fprintln(os.Stdout, "MOCK_NODE_STARTED_WILL_CRASH")
			time.Sleep(duration)
			fmt.Fprintln(os.Stdout, "MOCK_NODE_CRASH_AFTER_RUN")
			os.Exit(1)
		}
		_ = os.Remove(marker)
		fmt.Fprintln(os.Stdout, "MOCK_NODE_RECOVERED_RUNNING")
		runDuration := parseDurationEnv("MOCK_NODE_RUN_DURATION", 1*time.Second)
		time.Sleep(runDuration)
		fmt.Fprintln(os.Stdout, "MOCK_NODE_CLEAN_EXIT")
		os.Exit(0)
	default:
		os.Exit(0)
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
