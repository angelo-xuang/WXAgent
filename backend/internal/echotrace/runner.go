package echotrace

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
)

type Runner struct {
	cmd config.CommandSpec
}

func New(cmd config.CommandSpec) *Runner {
	return &Runner{cmd: cmd}
}

func (r *Runner) Run(extraArgs []string, silent bool) error {
	if r.cmd.Path == "" {
		return fmt.Errorf("echotrace command path not configured")
	}

	args := append([]string{}, r.cmd.Args...)
	if len(extraArgs) > 0 {
		args = append(args, extraArgs...)
	}

	cmd := exec.Command(r.cmd.Path, args...)
	cmd.Dir = r.cmd.Cwd
	cmd.Env = os.Environ()
	for k, v := range r.cmd.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// If silent, we might want to hide window on Windows, but for now just don't pipe stdout/stderr unless needed.
	// Or maybe we want to pipe them to log?
	if !silent {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}

	return cmd.Run()
}
