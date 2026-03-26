package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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
