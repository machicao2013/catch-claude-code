package launcher

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
)

func FindClaude() string {
	if cmd := os.Getenv("CLAUDE_CODE_TEAMMATE_COMMAND"); cmd != "" {
		if _, err := os.Stat(cmd); err == nil {
			return cmd
		}
	}
	if path, err := exec.LookPath("claude-internal"); err == nil {
		return path
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	return ""
}

func BuildEnv(proxyURL string, currentEnv []string) []string {
	var env []string
	for _, e := range currentEnv {
		if strings.HasPrefix(e, "ANTHROPIC_BASE_URL=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "ANTHROPIC_BASE_URL="+proxyURL)
	return env
}

// ExtractUpstreamURL tries to find the real API base URL from the claude binary.
// It scans the binary for known URL patterns. Returns empty string if not found.
func ExtractUpstreamURL(claudePath string) string {
	if claudePath == "" {
		return ""
	}

	f, err := os.Open(claudePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Match URLs like https://copilot.code.woa.com/.../codebuddy-code
	// or https://api.anthropic.com
	re := regexp.MustCompile(`https://[a-zA-Z0-9._-]+/[a-zA-Z0-9/_-]*codebuddy-code`)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		matches := re.FindAllString(scanner.Text(), -1)
		for _, m := range matches {
			// Prefer the non-offline endpoint
			if !strings.Contains(m, "offline") {
				return m
			}
		}
	}
	return ""
}

func Launch(claudePath string, args []string, env []string) (int, error) {
	if claudePath == "" {
		return 1, fmt.Errorf("claude-internal not found; install it or set CLAUDE_CODE_TEAMMATE_COMMAND")
	}

	cmd := exec.Command(claudePath, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start claude: %w", err)
	}

	err := cmd.Wait()
	signal.Stop(sigCh)
	close(sigCh)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
