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

// Cropped renvoie la variante recadrée sur l'objet d'un chemin de snapshot
// d'événement ; tout autre chemin (image en direct…) est rendu tel quel.
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

// RecordingClipPath renvoie le clip des enregistrements d'une caméra entre deux instants.
func RecordingClipPath(camera string, start, end float64) string {
	return fmt.Sprintf("/api/%s/start/%d/end/%d/clip.mp4",
		url.PathEscape(camera), int64(math.Floor(start)), int64(math.Ceil(end)))
}

// EventThumbnailPath renvoie la miniature (petite, recadrée sur l'objet) d'un événement.
func EventThumbnailPath(id string) string {
	return "/api/events/" + url.PathEscape(id) + "/thumbnail.jpg"
}
