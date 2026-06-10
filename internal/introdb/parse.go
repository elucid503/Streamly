package introdb

import "encoding/json"

func parseAPIErrorBody(body string) apiErrorBody {

	var parsed apiErrorBody
	_ = json.Unmarshal([]byte(body), &parsed)

	return parsed

}

func parseMediaResponse(raw mediaResponseRaw) *MediaRecord {

	record := &MediaRecord{
		TMDBId: raw.TMDBId,
		Type:   raw.Type,
	}

	for _, segment := range raw.Intro {
		record.Intro = append(record.Intro, normalizeSegment(segment))
	}

	return record

}

func normalizeSegment(raw segmentTimestampRaw) SegmentTimestamp {

	start := int64(0)
	if raw.StartMs != nil {
		start = *raw.StartMs
	}

	return SegmentTimestamp{
		StartMs: start,
		EndMs:   raw.EndMs,
	}

}
