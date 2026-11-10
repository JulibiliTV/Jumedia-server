package emby

import (
	"net/http"
	"net/url"
	"strconv"
)

// Provider selects the upstream protocol. Jumedia media references are scoped
// by datasource ID, so their format is shared by both providers.
type Provider string

const (
	Emby     Provider = "emby"
	Jellyfin Provider = "jellyfin"
	FnTV     Provider = "fntv"
	Plex     Provider = "plex"
)

func (p Provider) Name() string {
	if p == Plex {
		return "Plex"
	}
	if p == FnTV {
		return "飞牛影视"
	}
	if p == Jellyfin {
		return "Jellyfin"
	}
	return "Emby"
}

func (p Provider) AuthorizeHeader(h http.Header, token string) {
	const client = `MediaBrowser Client="Jumedia", Device="Jumedia Server", DeviceId="` + DeviceID + `", Version="0.1.0"`
	if p == Jellyfin {
		auth := client
		if token != "" {
			auth += ", Token=" + strconv.Quote(token)
		}
		h.Set("Authorization", auth)
	} else if token == "" {
		h.Set("X-Emby-Authorization", client)
	} else {
		h.Set("X-Emby-Token", token)
	}
}

func (p Provider) UserPath(user, resource string, q url.Values) string {
	if p == Jellyfin {
		q.Set("UserId", user)
		if resource == "Views" {
			return "/UserViews"
		}
		return "/Items"
	}
	return "/Users/" + url.PathEscape(user) + "/" + resource
}

func (p Provider) ItemFields() string {
	if p == Jellyfin {
		// Runtime and premiere date are standard DTO properties in Jellyfin.
		return "Path,Overview,Genres,DateCreated,MediaSources,MediaStreams"
	}
	return "Path,Overview,Genres,PremiereDate,DateCreated,MediaSources,RunTimeTicks"
}
