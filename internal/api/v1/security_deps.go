package v1

// Security dependencies arrive through optional With… setters, so a test can
// build a handler without the parts it isn't exercising. In production a nil
// one silently skips its check — the parental watch limit, the admin stop,
// the concurrent-stream cap, the downloads toggle, restore's password step-up
// — so each handler reports the ones it was built without, and the server
// refuses to start while any is missing (api.Handlers.ValidateSecurityDeps).
// The library ACL is covered separately (LibraryAccessWired) and also fails
// closed at request time.

// SecurityDepsMissing names the playback gates the item handler was built
// without: Progress / StreamFile / DownloadFile enforce all four.
func (h *ItemHandler) SecurityDepsMissing() []string {
	var m []string
	if h.watchLimit == nil {
		m = append(m, "watch limit (WithWatchLimit)")
	}
	if h.stops == nil {
		m = append(m, "admin stop (WithPlaybackStops)")
	}
	if h.streamLimit == nil {
		m = append(m, "stream limit (WithStreamLimit)")
	}
	if h.dlGate == nil {
		m = append(m, "downloads toggle (WithDownloadGate)")
	}
	return m
}

// SecurityDepsMissing names the gates transcode Start, the playback decision
// and the static-ABR routes were built without.
func (h *NativeTranscodeHandler) SecurityDepsMissing() []string {
	var m []string
	if h.watchLimit == nil {
		m = append(m, "watch limit (WithWatchLimit)")
	}
	if h.stops == nil {
		m = append(m, "admin stop (WithPlaybackStops)")
	}
	if h.streamLimit == nil {
		m = append(m, "stream limit (WithStreamLimit)")
	}
	return m
}

// SecurityDepsMissing names the gates converted downloads were built without.
func (h *DownloadsHandler) SecurityDepsMissing() []string {
	var m []string
	if h.dlGate == nil {
		m = append(m, "downloads toggle (WithDownloadGate)")
	}
	if h.watchLimit == nil {
		m = append(m, "watch limit (WithWatchLimit)")
	}
	if h.stops == nil {
		m = append(m, "admin stop (WithPlaybackStops)")
	}
	return m
}

// SecurityDepsMissing reports a restore built without its password / TOTP
// step-up: without it an admin session alone could replace the database.
func (h *BackupHandler) SecurityDepsMissing() []string {
	if h.reauth == nil {
		return []string{"restore re-authentication (WithReauth)"}
	}
	return nil
}

// SecurityDepsMissing reports an OPDS catalog built without the downloads
// toggle its acquisition links are gated by.
func (h *OPDSHandler) SecurityDepsMissing() []string {
	if h.dlGate == nil {
		return []string{"downloads toggle (WithDownloadGate)"}
	}
	return nil
}

// SecurityDepsMissing reports live TV built without the parental watch limit.
func (h *LiveTVHandler) SecurityDepsMissing() []string {
	if h.watchLimit == nil {
		return []string{"watch limit (WithWatchLimit)"}
	}
	return nil
}

// SecurityDepsMissing reports watch statuses built without their item gate.
func (h *WatchStatusHandler) SecurityDepsMissing() []string {
	if h.items == nil {
		return []string{"item gate (WithItemGate)"}
	}
	return nil
}
