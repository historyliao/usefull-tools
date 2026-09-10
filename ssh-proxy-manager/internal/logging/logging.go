package logging

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const tailWindow = 64 * 1024

func Path(dir, name string) string {
	return filepath.Join(dir, name+".log")
}

func Append(path, line string) error {
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(line)
	return err
}

func Tail(path string, lines int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	offset := int64(0)
	if size > tailWindow {
		offset = size - tailWindow
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		return nil, nil
	}
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all, nil
}

func Follow(ctx context.Context, path string, out io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			<-ctx.Done()
			return nil
		}
		return err
	}
	defer file.Close()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return err
		}
	}
}
