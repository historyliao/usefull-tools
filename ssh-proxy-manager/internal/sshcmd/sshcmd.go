package sshcmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
)

func Args(bin string, def config.Definition) []string {
	if bin == "" {
		bin = "ssh"
	}
	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
	}
	if def.Target.Port != 0 && def.Target.Port != config.DefaultSSHPort {
		args = append(args, "-p", strconv.Itoa(def.Target.Port))
	}
	if def.Target.Identity != "" {
		args = append(args, "-i", expand(def.Target.Identity))
	}
	for _, f := range def.Forwards {
		args = append(args, forwardArgs(f)...)
	}
	args = append(args, def.Target.ExtraArgs...)
	args = append(args, def.Target.Destination())
	return append([]string{bin}, args...)
}

func forwardArgs(f config.Forward) []string {
	switch f.Type {
	case "D":
		return []string{"-D", f.Spec()}
	default:
		return []string{"-" + f.Type, f.Spec()}
	}
}

func expand(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
