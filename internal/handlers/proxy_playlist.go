package handlers

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"node-content/internal/db/models"

	"go.mongodb.org/mongo-driver/bson"
)

var proxyPlaylistURI = regexp.MustCompile(`\bURI="([^"\r\n]*)"`)

// renderProxyMediaPlaylist keeps playlist ownership in node-content. Only the
// generated segment/resource URLs point at node-proxy.
func renderProxyMediaPlaylist(media models.Media, proxyBaseURL string) (string, error) {
	descriptor := metadataObject(media.Metadata["media"])
	if len(descriptor) == 0 {
		hls := metadataObject(media.Metadata["hls"])
		descriptor = metadataObject(hls["media"])
	}
	if len(descriptor) == 0 {
		return "", errors.New("proxy playlist descriptor is missing")
	}
	if descriptor["runs"] == nil {
		descriptor["runs"] = media.Metadata["runs"]
	}

	version := metadataNumber(descriptor, "version")
	if version <= 0 {
		version = 3
	}
	sequence := metadataNumber(descriptor, "mediaSequence")
	if version < 0 || sequence < 0 {
		return "", errors.New("invalid proxy playlist header")
	}

	type segment struct {
		duration float64
		title    string
		tags     []string
	}
	segments := make([]segment, 0)
	appendSegment := func(item map[string]interface{}) error {
		duration := metadataNumber(item, "duration")
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return errors.New("invalid proxy segment duration")
		}
		title, _ := item["title"].(string)
		if strings.ContainsAny(title, "\r\n") {
			return errors.New("invalid proxy segment title")
		}
		segments = append(segments, segment{duration: duration, title: title, tags: stringValues(item["tags"])})
		if len(segments) > 100000 {
			return errors.New("proxy playlist exceeds 100000 segments")
		}
		return nil
	}

	if template, _ := descriptor["segmentTemplate"].(string); template != "" {
		if strings.Count(template, "{sequence}") != 1 {
			return "", errors.New("invalid proxy segment template")
		}
		previous := -1
		for _, run := range objectValues(descriptor["runs"]) {
			from, to := metadataNumber(run, "from"), metadataNumber(run, "to")
			if math.Trunc(from) != from || math.Trunc(to) != to || from < 0 || to < from || to-from > 100000 || int(from) <= previous {
				return "", errors.New("invalid proxy segment run")
			}
			for index := int(from); index <= int(to); index++ {
				if err := appendSegment(run); err != nil {
					return "", err
				}
			}
			previous = int(to)
		}
	} else {
		for _, item := range objectValues(descriptor["segments"]) {
			if err := appendSegment(item); err != nil {
				return "", err
			}
		}
	}
	if len(segments) == 0 {
		return "", errors.New("proxy playlist has no segments")
	}
	if expected := metadataNumber(descriptor, "segmentCount"); expected > 0 && int(expected) != len(segments) {
		return "", errors.New("proxy segment count does not match descriptor")
	}

	targetDuration := metadataNumber(descriptor, "targetDuration")
	if targetDuration <= 0 {
		for _, item := range segments {
			if item.duration > targetDuration {
				targetDuration = item.duration
			}
		}
		targetDuration = math.Ceil(targetDuration)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", int(version), int(targetDuration), int(sequence))
	if playlistType, _ := descriptor["playlistType"].(string); playlistType != "" {
		if playlistType != "VOD" && playlistType != "EVENT" {
			return "", errors.New("invalid proxy playlist type")
		}
		output.WriteString("#EXT-X-PLAYLIST-TYPE:" + playlistType + "\n")
	}
	if independent, _ := descriptor["independentSegments"].(bool); independent {
		output.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	}

	resourceIndex := 0
	proxyResource := func() (string, error) {
		result, err := url.JoinPath(proxyBaseURL, media.Slug, "resource", strconv.Itoa(resourceIndex))
		resourceIndex++
		return result, err
	}
	if initSegment := metadataObject(descriptor["initSegment"]); len(initSegment) > 0 {
		resourceURL, err := proxyResource()
		if err != nil {
			return "", err
		}
		output.WriteString(`#EXT-X-MAP:URI="` + resourceURL + `"`)
		if byteRange, _ := initSegment["byteRange"].(string); byteRange != "" {
			if strings.ContainsAny(byteRange, "\"\r\n") {
				return "", errors.New("invalid proxy init byte range")
			}
			output.WriteString(`,BYTERANGE="` + byteRange + `"`)
		}
		output.WriteByte('\n')
	}

	for index, item := range segments {
		for _, tag := range item.tags {
			if !strings.HasPrefix(tag, "#") || strings.ContainsAny(tag, "\r\n") {
				return "", errors.New("invalid proxy segment tag")
			}
			var rewriteErr error
			tag = proxyPlaylistURI.ReplaceAllStringFunc(tag, func(string) string {
				resourceURL, err := proxyResource()
				if err != nil {
					rewriteErr = err
					return ""
				}
				return `URI="` + resourceURL + `"`
			})
			if rewriteErr != nil {
				return "", rewriteErr
			}
			output.WriteString(tag + "\n")
		}
		fmt.Fprintf(&output, "#EXTINF:%s,%s\n", strconv.FormatFloat(item.duration, 'f', -1, 64), item.title)
		segmentURL, err := url.JoinPath(proxyBaseURL, media.Slug, fmt.Sprintf("v-%d.jpeg", index))
		if err != nil {
			return "", err
		}
		output.WriteString(segmentURL + "\n")
		resourceIndex++ // node-proxy indexes every source URI, including segments.
	}
	if endList, _ := descriptor["endList"].(bool); endList {
		output.WriteString("#EXT-X-ENDLIST\n")
	}
	return output.String(), nil
}

func objectValues(value interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0)
	switch values := value.(type) {
	case []map[string]interface{}:
		return values
	case []interface{}:
		for _, item := range values {
			if object := metadataObject(item); object != nil {
				result = append(result, object)
			}
		}
	case bson.A:
		for _, item := range values {
			if object := metadataObject(item); object != nil {
				result = append(result, object)
			}
		}
	}
	return result
}

func stringValues(value interface{}) []string {
	result := make([]string, 0)
	switch values := value.(type) {
	case []string:
		return values
	case []interface{}:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	case bson.A:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}
