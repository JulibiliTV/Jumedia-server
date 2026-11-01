package api

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const imageCacheLimit = 10 << 20

var imageCacheShardPattern = regexp.MustCompile(`^[0-9a-f]{2}$`)
var imageCacheFilePattern = regexp.MustCompile(`^([0-9a-f]{64})(\.type|\.tmp-[0-9a-f]{32})?$`)

type imageCacheMetadata struct {
	Type     string    `json:"type"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
}

type imageCacheEntry struct {
	name     string
	size     int64
	accessed time.Time
}

// All operations after opening the root are relative to directory descriptors.
// O_NOFOLLOW also rejects symlinks pointing *inside* the cache (os.Root alone
// permits those). No recursive removal or path-based rename is used.
func imageCacheRoot(dir string, create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), dir), nil
}
func imageCacheOpen(parent *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err == nil && !directory && !st.Mode().IsRegular() {
		err = errors.New("not a regular cache file")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func imageCacheShard(root *os.File, key string, create bool) (*os.File, error) {
	if create {
		if err := unix.Mkdirat(int(root.Fd()), key[:2], 0755); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
	}
	return imageCacheOpen(root, key[:2], true)
}

// visit only recognizes our two-level namespace. Unknown files and symlinks
// (including cache-shaped symlinks) are left untouched.
func imageCacheWalk(dir string, visit func(*os.File, string, os.FileInfo) error, prune bool) error {
	root, err := imageCacheRoot(dir, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	shards, err := root.ReadDir(-1)
	if err != nil {
		return err
	}
	var result error
	for _, entry := range shards {
		if !entry.IsDir() || !imageCacheShardPattern.MatchString(entry.Name()) {
			continue
		}
		shard, err := imageCacheOpen(root, entry.Name(), true)
		if err != nil {
			continue
		}
		files, err := shard.ReadDir(-1)
		if err != nil {
			result = errors.Join(result, err)
		}
		removed := false
		for _, file := range files {
			match := imageCacheFilePattern.FindStringSubmatch(file.Name())
			if match == nil || match[1][:2] != entry.Name() || !file.Type().IsRegular() {
				continue
			}
			f, err := imageCacheOpen(shard, file.Name(), false)
			if err != nil {
				continue
			}
			st, err := f.Stat()
			f.Close()
			if err == nil {
				visitErr := visit(shard, file.Name(), st)
				removed = removed || visitErr == nil
				result = errors.Join(result, visitErr)
			}
		}
		shard.Close()
		if prune && removed {
			_ = unix.Unlinkat(int(root.Fd()), entry.Name(), unix.AT_REMOVEDIR)
		}
	}
	return result
}

func imageCacheRead(dir, key string) (*os.File, imageCacheMetadata, int64, error) {
	var meta imageCacheMetadata
	root, err := imageCacheRoot(dir, false)
	if err != nil {
		return nil, meta, 0, err
	}
	defer root.Close()
	shard, err := imageCacheShard(root, key, false)
	if err != nil {
		return nil, meta, 0, err
	}
	defer shard.Close()
	f, err := imageCacheOpen(shard, key, false)
	if err != nil {
		return nil, meta, 0, err
	}
	// Bound malformed metadata, including manually placed cache-shaped files.
	reader := bufio.NewReader(io.LimitReader(f, 4096))
	line, err := reader.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &meta)
	}
	st, statErr := f.Stat()
	if err == nil {
		err = statErr
	}
	offset := int64(len(line))
	if err == nil && (!strings.HasPrefix(strings.ToLower(meta.Type), "image/") || meta.Created.IsZero() || st.Size() < offset || st.Size()-offset > imageCacheLimit) {
		err = errors.New("invalid image cache metadata")
	}
	if err != nil {
		f.Close()
		return nil, meta, 0, err
	}
	return f, meta, offset, nil
}

func imageCacheWrite(dir, key string, meta imageCacheMetadata, body []byte, now time.Time) (int64, error) {
	root, err := imageCacheRoot(dir, true)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	shard, err := imageCacheShard(root, key, true)
	if err != nil {
		return 0, err
	}
	defer shard.Close()
	// Never replace a cache-shaped symlink or another non-regular entry.
	var st unix.Stat_t
	err = unix.Fstatat(int(shard.Fd()), key, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, errors.New("cache target is not a regular file")
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return 0, err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return 0, err
	}
	name := key + ".tmp-" + hex.EncodeToString(nonce)
	fd, err := unix.Openat(int(shard.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return 0, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	defer unix.Unlinkat(int(shard.Fd()), name, 0)
	header, err := json.Marshal(meta)
	if err != nil {
		return 0, err
	}
	header = append(header, '\n')
	if _, err = f.Write(header); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = imageCacheTouch(f, now)
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return 0, err
	}
	if err = unix.Renameat(int(shard.Fd()), name, int(shard.Fd()), key); err != nil {
		return 0, err
	}
	return int64(len(header) + len(body)), nil
}

func imageCacheTouch(f *os.File, now time.Time) error {
	// Descriptor-based equivalent of os.Chtimes; never follows a replaced path.
	tv := unix.NsecToTimeval(now.UnixNano())
	return unix.Futimes(int(f.Fd()), []unix.Timeval{tv, tv})
}

func imageCacheRemove(dir, name string) error {
	root, err := imageCacheRoot(dir, false)
	if err != nil {
		return err
	}
	defer root.Close()
	shard, err := imageCacheShard(root, name, false)
	if err != nil {
		return err
	}
	defer shard.Close()
	f, err := imageCacheOpen(shard, name, false)
	if err != nil {
		return err
	}
	f.Close()
	err = unix.Unlinkat(int(shard.Fd()), name, 0)
	if err == nil {
		_ = unix.Unlinkat(int(root.Fd()), name[:2], unix.AT_REMOVEDIR)
	}
	return err
}

func imageCacheProbe(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".jumedia-write-")
	if err != nil {
		return err
	}
	_, err = f.WriteString("probe")
	return errors.Join(err, f.Close(), os.Remove(f.Name()))
}

func imageCacheDefaultDir(dataDir string) (string, error) {
	dir, err := filepath.Abs(filepath.Join(dataDir, "image-cache"))
	if err != nil {
		return "", fmt.Errorf("image cache directory: %w", err)
	}
	return dir, nil
}
