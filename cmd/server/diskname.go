package main

import "regexp"

// The on-disk name is `<slug><extension>`. The extension is uploader-supplied, so it is gated here before it
// can touch disk; the URL is the slug alone and never carries it.

var extRe = regexp.MustCompile(`^\.[A-Za-z0-9._-]{1,16}$`)

func sanitiseExt(ext string) string {
	if ext == "" {
		return ""
	}
	if !extRe.MatchString(ext) {
		return ""
	}
	return ext
}
