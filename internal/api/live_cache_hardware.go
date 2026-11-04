package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const liveCacheRenderDevice = "/dev/dri/renderD128"

// A real frame checks permissions, driver initialization and H.264 encoding;
// listing encoders alone only checks how FFmpeg was built.
func liveCacheVAAPIProbe(ctx context.Context, binary, device string, height int) error {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	_ = f.Close()
	height = max(2, height/2*2)
	width := max(2, height*16/9/2*2)
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-vaapi_device", device, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=25", width, height)}
	args = append(args, liveCacheVideoArgs(height, "", "h264_vaapi", false)...)
	args = append(args, "-frames:v", "1", "-an", "-f", "null", "-")
	cmd := exec.CommandContext(probeCtx, binary, args...)
	cmd.WaitDelay = time.Second
	var stderr liveCacheTail
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return probeCtx.Err()
		}
		if len(stderr.data) > 0 {
			return errors.New(liveCacheError(string(stderr.data)))
		}
		return err
	}
	return nil
}

func (s *Server) encodeLiveCache(ctx context.Context, x *liveCache, p liveCacheProbe, input, binary, encoder, device string, toneMap bool, stallTimeout time.Duration) (int64, error) {
	automatic := encoder == "auto"
	if automatic {
		height := x.Height
		for _, stream := range p.Streams {
			if stream.Type == "video" {
				height = min(height, stream.Height)
				break
			}
		}
		encoder = "libx264"
		if err := liveCacheVAAPIProbe(ctx, binary, device, height); err == nil {
			encoder = "h264_vaapi"
		} else if !os.IsNotExist(err) && ctx.Err() == nil {
			s.Logger.Info("直播缓存硬件探测未通过，使用软件编码", "key", x.Key, "error", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.Logger.Info("直播缓存编码器", "key", x.Key, "encoder", encoder)
	args, err := liveCacheArgs(x, p, input, encoder, device, toneMap)
	if err != nil {
		return 0, err
	}
	size, err := s.runLiveCacheTranscode(ctx, x, binary, args, stallTimeout)
	if err == nil || !automatic || encoder != "h264_vaapi" || ctx.Err() != nil {
		return size, err
	}
	s.Logger.Warn("直播缓存硬件编码失败，重试软件编码", "key", x.Key, "error", err)
	if err := liveCacheRemove(x); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(x.Dir+".tmp", 0755); err != nil {
		return 0, err
	}
	if _, err := s.Store.DB.ExecContext(ctx, "UPDATE live_caches SET progress=0,mtime=? WHERE key=? AND status='running'", time.Now().Unix(), x.Key); err != nil {
		return 0, err
	}
	args, err = liveCacheArgs(x, p, input, "libx264", device, toneMap)
	if err != nil {
		return 0, err
	}
	return s.runLiveCacheTranscode(ctx, x, binary, args, stallTimeout)
}
