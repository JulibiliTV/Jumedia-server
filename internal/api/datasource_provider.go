package api

import (
	"context"
	"net/http"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/fntv"
	"jumedia-server/internal/plex"
)

type sourceMediaClient interface {
	Probe(context.Context) (emby.ServerInfo, error)
	Image(context.Context, string, string, string) (*http.Response, error)
	Stream(context.Context, string, string, http.Header) (*http.Response, error)
	RawStream(context.Context, string, string, string, http.Header) (*http.Response, error)
	HeadStream(context.Context, string, string, string, http.Header) (*http.Response, error)
	StreamURL(context.Context, string, string, string) (string, error)
	Close()
}

func newSourceMediaClient(provider emby.Provider, address, username, password string, id int64, allowPrivate bool) (sourceMediaClient, error) {
	if provider == emby.Plex {
		return plex.New(address, username, password, allowPrivate)
	}
	if provider == emby.FnTV {
		return fntv.New(address, username, password, allowPrivate)
	}
	return provider.NewDataSource(address, username, password, id, allowPrivate)
}
