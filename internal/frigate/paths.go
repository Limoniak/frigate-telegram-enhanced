package frigate

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

func EventSnapshotPath(id string) string {
	return "/api/events/" + url.PathEscape(id) + "/snapshot.jpg?bbox=1"
}

// EventSnapshotHeightPath is the snapshot scaled down to h pixels high: enough for the
// interface, and well under the size bound of what it relays.
func EventSnapshotHeightPath(id string, h int) string {
	return fmt.Sprintf("%s&h=%d", EventSnapshotPath(id), h)
}

// Cropped returns the variant of an event snapshot path cropped on the object; any
// other path (live image…) is returned as is.
func Cropped(snapshotPath string) string {
	if strings.HasSuffix(snapshotPath, "/snapshot.jpg?bbox=1") {
		return snapshotPath + "&crop=1&quality=90"
	}
	return snapshotPath
}

func EventClipPath(id string) string { return "/api/events/" + url.PathEscape(id) + "/clip.mp4" }

func EventGIFPath(id string) string { return "/api/events/" + url.PathEscape(id) + "/preview.gif" }

func ReviewGIFPath(id string) string {
	return "/api/review/" + url.PathEscape(id) + "/preview?format=gif"
}

func LatestPath(camera string) string { return "/api/" + url.PathEscape(camera) + "/latest.jpg" }

// RecordingClipPath returns the clip of a camera's recordings between two instants.
func RecordingClipPath(camera string, start, end float64) string {
	return fmt.Sprintf("/api/%s/start/%d/end/%d/clip.mp4",
		url.PathEscape(camera), int64(math.Floor(start)), int64(math.Ceil(end)))
}

// EventThumbnailPath returns the thumbnail (small, cropped on the object) of an event.
func EventThumbnailPath(id string) string {
	return "/api/events/" + url.PathEscape(id) + "/thumbnail.jpg"
}
