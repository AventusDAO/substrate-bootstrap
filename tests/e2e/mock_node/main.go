package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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
		marker := os.Getenv("MOCK_NODE_MARKER")
		if marker == "" {
			marker = "/tmp/mock_node_marker"
		}
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			_ = os.WriteFile(marker, []byte("1"), 0o644)
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
		marker := os.Getenv("MOCK_NODE_MARKER")
		if marker == "" {
			marker = "/tmp/mock_node_crash_after_marker"
		}
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			_ = os.WriteFile(marker, []byte("1"), 0o644)
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
