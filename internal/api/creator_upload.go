package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) creatorUploadVideo(c *Ctx) (any, error) {
	return s.creatorUpload(c, false)
}

func (s *Server) creatorUploadImage(c *Ctx) (any, error) {
	return s.creatorUpload(c, true)
}

func (s *Server) creatorUpload(c *Ctx, image bool) (any, error) {
	if _, err := c.CSRF(); err != nil {
		return nil, err
	}
	missing := "请选择视频文件"
	if image {
		missing = "请选择图片"
	}
	if c.Form == nil || len(c.Form.File["file"]) == 0 {
		return nil, Fail(-400, missing)
	}
	header := c.Form.File["file"][0]
	setting, err := s.Store.Setting(c.Context(), "maxUploadMB")
	if err != nil {
		return nil, err
	}
	limit := NumberGo(fmt.Sprint(setting), 0) * 1024 * 1024
	if float64(header.Size) > limit {
		return nil, Fail(-400, fmt.Sprintf("文件超过 %vMB 上限", setting))
	}
	src, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if err = os.MkdirAll(s.Config.UploadDir(), 0755); err != nil {
		return nil, err
	}
	// Keep a safe extension for the static file server's media content type;
	// no part of the user-provided basename becomes a filesystem path.
	ext := filepath.Ext(header.Filename)
	if len(ext) > 16 || strings.IndexFunc(ext, func(r rune) bool {
		return r != '.' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	}) >= 0 {
		ext = ""
	}
	if image {
		header := make([]byte, 512)
		n, readErr := io.ReadFull(src, header)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, readErr
		}
		ext = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"}[http.DetectContentType(header[:n])]
		if ext == "" {
			return nil, Fail(-400, "仅支持 PNG、JPEG、WebP 或 GIF 图片")
		}
		if _, err = src.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	name := "media-" + hex.EncodeToString(random) + ext
	dst, err := os.OpenFile(filepath.Join(s.Config.UploadDir(), name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		dst.Close()
		if !complete {
			_ = os.Remove(dst.Name())
		}
	}()
	if _, err = io.Copy(dst, src); err != nil {
		return nil, err
	}
	if err = dst.Close(); err != nil {
		return nil, err
	}
	complete = true
	ref := "/upload/" + filepath.Base(dst.Name())
	if image {
		return map[string]any{"url": ref}, nil
	}
	return map[string]any{"filename": ref, "name": header.Filename, "size": header.Size}, nil
}
