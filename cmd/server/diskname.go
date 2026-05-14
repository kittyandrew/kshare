package main

import "regexp"

// Rules for what's a valid on-disk filename. The URL identity is the
// slug alone (`/s/<slug>`, validated by api.SlugRe); the on-disk
// filename is `<slug><extension>` for grep-ability when an operator
// browses /data/files/. extRe + sanitiseExt gate the uploader-supplied
// extension before it ever touches disk.

// extRe is the strict allowlist for the trailing extension applied
// to on-disk names. Only [A-Za-z0-9._-] inside, max 16 chars.
var extRe = regexp.MustCompile(`^\.[A-Za-z0-9._-]{1,16}$`)

// sanitiseExt returns ext if it's a safe trailing extension, ""
// otherwise. Output is the extension that gets joined onto the slug
// for the on-disk filename; the URL never carries it.
func sanitiseExt(ext string) string {
	if ext == "" {
		return ""
	}
	if !extRe.MatchString(ext) {
		return ""
	}
	return ext
}
