// Package volagent is the side of the volume explorer that runs inside a
// helper container, where the volume is mounted at Root. DocMan's own binary
// is the helper's only program, so these few operations are all it can do:
// wait, list a directory, make one, rename, and delete. Files themselves move
// through the Engine's archive API instead.
//
// Every path is relative to Root and cleaned first, so nothing outside the
// volume can be named. Symbolic links are reported, never followed.
package volagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Root is where the helper container mounts the volume.
const Root = "/volume"

// Command is the first argument that selects the agent in the docman binary.
const Command = "volume-agent"

// maxEntries caps a listing so a directory with millions of files stays usable.
const maxEntries = 5000

// Entry is one item in a directory listing.
type Entry struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // dir, file, link or other
	Size    int64  `json:"size"`
	Mode    string `json:"mode"` // like ls: drwxr-xr-x
	Perm    uint32 `json:"perm"` // permission bits, e.g. 0644
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
	ModTime int64  `json:"mtime"`
	Target  string `json:"target,omitempty"` // for links
}

// Listing is the reply to ls.
type Listing struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated,omitempty"`
	Dir       Entry   `json:"dir"`
}

// Resolve turns a volume path such as "/config/app.yml" into a path under
// Root, refusing anything that would leave it.
func Resolve(p string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	if strings.Contains(clean, "\x00") {
		return "", errors.New("invalid path")
	}
	return path.Join(Root, clean), nil
}

// Run executes one agent command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: docman volume-agent serve|ls|mkdir|mv|rm ...")
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		// Keep the helper alive; DocMan removes it when it is no longer used.
		for {
			time.Sleep(time.Hour)
		}
	case "ls":
		err = list(arg(args, 1), stdout)
	case "mkdir":
		err = mkdir(arg(args, 1))
	case "mv":
		err = move(arg(args, 1), arg(args, 2))
	case "rm":
		err = remove(arg(args, 1))
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func describe(name string, info os.FileInfo, full string) Entry {
	e := Entry{
		Name: name, Size: info.Size(), Mode: info.Mode().String(),
		Perm: uint32(info.Mode().Perm()), ModTime: info.ModTime().Unix(),
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		e.Type = "link"
		if t, err := os.Readlink(full); err == nil {
			e.Target = t
		}
	case info.IsDir():
		e.Type = "dir"
		e.Size = 0
	case info.Mode().IsRegular():
		e.Type = "file"
	default:
		e.Type = "other"
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = int(st.Uid), int(st.Gid)
	}
	return e
}

func list(p string, w io.Writer) error {
	full, err := Resolve(p)
	if err != nil {
		return err
	}
	dirInfo, err := os.Lstat(full)
	if err != nil {
		return friendly(err)
	}
	if !dirInfo.IsDir() {
		return errors.New("not a folder")
	}
	f, err := os.Open(full)
	if err != nil {
		return friendly(err)
	}
	defer f.Close()
	names, err := f.Readdirnames(maxEntries + 1)
	if err != nil && err != io.EOF {
		return friendly(err)
	}
	out := Listing{Path: strings.TrimPrefix(full, Root), Entries: []Entry{}}
	if out.Path == "" {
		out.Path = "/"
	}
	out.Dir = describe(path.Base(out.Path), dirInfo, full)
	if len(names) > maxEntries {
		names = names[:maxEntries]
		out.Truncated = true
	}
	for _, name := range names {
		child := filepath.Join(full, name)
		info, err := os.Lstat(child)
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, describe(name, info, child))
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return json.NewEncoder(w).Encode(out)
}

func mkdir(p string) error {
	full, err := Resolve(p)
	if err != nil {
		return err
	}
	if full == Root {
		return errors.New("choose a folder name")
	}
	if _, err := os.Lstat(full); err == nil {
		return errors.New("something with that name already exists")
	}
	// New folders take the owner of the folder they are made in, so an
	// application running as a non-root user can still write to them.
	parent, err := os.Stat(path.Dir(full))
	if err != nil {
		return friendly(err)
	}
	if err := os.Mkdir(full, 0o755); err != nil {
		return friendly(err)
	}
	if st, ok := parent.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(full, int(st.Uid), int(st.Gid))
	}
	return nil
}

func move(from, to string) error {
	src, err := Resolve(from)
	if err != nil {
		return err
	}
	dst, err := Resolve(to)
	if err != nil {
		return err
	}
	if src == Root || dst == Root {
		return errors.New("the top of the volume cannot be moved")
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("something with that name already exists")
	}
	if strings.HasPrefix(dst+"/", src+"/") {
		return errors.New("a folder cannot be moved inside itself")
	}
	return friendly(os.Rename(src, dst))
}

func remove(p string) error {
	full, err := Resolve(p)
	if err != nil {
		return err
	}
	if full == Root {
		return errors.New("the top of the volume cannot be deleted")
	}
	if _, err := os.Lstat(full); err != nil {
		return friendly(err)
	}
	return friendly(os.RemoveAll(full))
}

// friendly turns a system error into a short sentence without the helper's
// internal paths.
func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrNotExist):
		return errors.New("not found: nothing by that name is in the volume")
	case errors.Is(err, os.ErrPermission):
		return errors.New("permission denied")
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}
