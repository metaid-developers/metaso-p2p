package publishedcontent

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixtureAUkiyoeManifest mirrors the real on-chain manifest of Motion Video
// fixture A (metaapp://22ce6ca2...i0): 10s silent Ukiyo-e example, videoFile
// without filename extension, no frames/hasAudio/loudnessLufs declarations.
const fixtureAUkiyoeManifest = `{
  "title": "霧海の渡し · Ukiyo-e Motion Video #001",
  "appName": "ukiyoe-motion-video-001",
  "tags": ["motion-video", "ukiyoe", "lemo-opuscar", "code-rendered-video", "tutorial"],
  "runtime": "browser",
  "version": "v1.0.0",
  "content": "metafile://c3f438d2f3ad99e06de4ccfd580dbc3dec31837b880b6fdf8a979d2043a04859i0.zip",
  "indexFile": "index.html",
  "prompt": "你是用代码拍电影的导演。请严格按以下步骤执行。",
  "metadata": {
    "author": "idq15dvr9rqh40n5a5r9al3fxepdsd4zkfyfmhd36k",
    "durationSec": 10,
    "engine": "lemo-opuscar",
    "episode": 1,
    "fps": 24,
    "madeAt": "2026-10-24",
    "resolution": "1920x1080",
    "series": "motion-video",
    "sourceIncluded": true,
    "style": "ukiyoe",
    "videoFile": "metafile://726ba90bff2f802e14febd92d6a8c0f95a460fff1ad17e783f3af312b56accc8i0",
    "videoSha256": "b8113ccc77c592466f8c67b7ff775cb9c57927a516c34989e8611a1d09f81674"
  }
}`

// fixtureBSilentFilmManifest mirrors the real on-chain manifest of Motion
// Video fixture B (metaapp://5686db0d...i0): ~47s silent-film example with
// music, videoFile WITH .mp4 extension, frames/hasAudio/loudnessLufs
// declared.
const fixtureBSilentFilmManifest = `{
  "title": "一阵风 · A GUST OF WIND · Silent Film Motion Video #002",
  "appName": "silent-film-motion-video-002",
  "tags": ["motion-video", "silent-film", "lemo-opuscar", "code-rendered-video", "tutorial"],
  "runtime": "browser",
  "version": "v1.0.0",
  "content": "metafile://e8cf698d384137fde19b05322f6bffd77e6f1a722edbe0592558bc1c8aad6bbci0.zip",
  "indexFile": "index.html",
  "prompt": "你是用代码拍电影的导演兼影院琴师。请严格按以下步骤执行。",
  "metadata": {
    "author": "idq15dvr9rqh40n5a5r9al3fxepdsd4zkfyfmhd36k",
    "durationSec": 47.216,
    "engine": "lemo-opuscar",
    "episode": 2,
    "fps": 24,
    "frames": 1133,
    "hasAudio": true,
    "loudnessLufs": -14.6,
    "madeAt": "2026-10-24",
    "resolution": "1920x1080",
    "series": "motion-video",
    "sourceIncluded": true,
    "style": "silent-film",
    "videoFile": "metafile://10b125b02fbaef807e5ebbf108107224b076f51096c80def1ceaad5c67ed2914i0.mp4",
    "videoSha256": "39c9c6c9b2a035568a07273b92b3f426d383a5ab5c1d98dfb7bd760faf0e1860"
  }
}`

const fixtureAVideoSha = "b8113ccc77c592466f8c67b7ff775cb9c57927a516c34989e8611a1d09f81674"
const fixtureBVideoSha = "39c9c6c9b2a035568a07273b92b3f426d383a5ab5c1d98dfb7bd760faf0e1860"

func processMotionVideoFixtures(t *testing.T, agg *Aggregator) {
	t.Helper()
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0",
		Path:         PathMetaApp,
		Timestamp:    1000,
		ContentType:  "application/json",
		ContentBody:  []byte(fixtureAUkiyoeManifest),
		GlobalMetaId: "gid-author",
		MetaId:       "meta-author",
		Address:      "addr-author",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "5686db0dd51ac19ec60c4a02ff66bc51adcf0149b8484141b38e730c734d9bb4i0",
		Path:         PathMetaApp,
		Timestamp:    2000,
		ContentType:  "application/json",
		ContentBody:  []byte(fixtureBSilentFilmManifest),
		GlobalMetaId: "gid-author",
		MetaId:       "meta-author",
		Address:      "addr-author",
	}))
}

func TestMotionVideoProjectionBothFixtures(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	processMotionVideoFixtures(t, agg)

	result, err := agg.ListMetaApps(MetaAppListParams{Tag: "motion-video", ChainName: "mvc"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len: got %d want 2", len(result.Items))
	}

	byRoot := map[string]MetaAppItem{}
	for _, item := range result.Items {
		byRoot[item.PinID] = item
	}

	a := byRoot["22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0"]
	if a.MotionVideo == nil {
		t.Fatal("fixture A motionVideo projection missing")
	}
	// Root/current mapping: create-only apps fold to themselves.
	if a.PinID != a.SourcePinID || a.CurrentPinID != a.PinID {
		t.Fatalf("fixture A identity: pinId=%q sourcePinId=%q currentPinId=%q", a.PinID, a.SourcePinID, a.CurrentPinID)
	}
	mv := a.MotionVideo
	if mv.VideoFile != "metafile://726ba90bff2f802e14febd92d6a8c0f95a460fff1ad17e783f3af312b56accc8i0" {
		t.Fatalf("fixture A videoFile: %q", mv.VideoFile)
	}
	if mv.VideoSha256 != fixtureAVideoSha {
		t.Fatalf("fixture A videoSha256: %q", mv.VideoSha256)
	}
	if mv.DurationSec == nil || *mv.DurationSec != 10 {
		t.Fatalf("fixture A durationSec: %v", mv.DurationSec)
	}
	if mv.FPS == nil || *mv.FPS != 24 {
		t.Fatalf("fixture A fps: %v", mv.FPS)
	}
	if mv.Resolution != "1920x1080" || mv.Engine != "lemo-opuscar" || mv.Style != "ukiyoe" {
		t.Fatalf("fixture A playback fields: %+v", mv)
	}
	// Missing audio metadata stays unknown (null), never false or zero.
	if mv.HasAudio != nil {
		t.Fatalf("fixture A hasAudio must stay null, got %v", *mv.HasAudio)
	}
	if mv.Frames != nil {
		t.Fatalf("fixture A frames must stay null, got %v", *mv.Frames)
	}
	if mv.LoudnessLufs != nil {
		t.Fatalf("fixture A loudnessLufs must stay null, got %v", *mv.LoudnessLufs)
	}
	if !mv.Learning.SourceIncluded || !mv.Learning.HasPrompt || mv.Learning.Status != LearningStatusDeclared {
		t.Fatalf("fixture A learning: %+v", mv.Learning)
	}

	b := byRoot["5686db0dd51ac19ec60c4a02ff66bc51adcf0149b8484141b38e730c734d9bb4i0"]
	if b.MotionVideo == nil {
		t.Fatal("fixture B motionVideo projection missing")
	}
	bmv := b.MotionVideo
	if bmv.VideoFile != "metafile://10b125b02fbaef807e5ebbf108107224b076f51096c80def1ceaad5c67ed2914i0.mp4" {
		t.Fatalf("fixture B videoFile (optional extension preserved): %q", bmv.VideoFile)
	}
	if bmv.VideoSha256 != fixtureBVideoSha {
		t.Fatalf("fixture B videoSha256: %q", bmv.VideoSha256)
	}
	if bmv.DurationSec == nil || *bmv.DurationSec != 47.216 {
		t.Fatalf("fixture B durationSec: %v", bmv.DurationSec)
	}
	if bmv.Frames == nil || *bmv.Frames != 1133 {
		t.Fatalf("fixture B frames: %v", bmv.Frames)
	}
	if bmv.HasAudio == nil || !*bmv.HasAudio {
		t.Fatalf("fixture B hasAudio must be true, got %v", bmv.HasAudio)
	}
	if bmv.LoudnessLufs == nil || *bmv.LoudnessLufs != -14.6 {
		t.Fatalf("fixture B loudnessLufs: %v", bmv.LoudnessLufs)
	}
	if bmv.Learning.Status != LearningStatusDeclared {
		t.Fatalf("fixture B learning: %+v", bmv.Learning)
	}

	// The emitted wire block keeps nulls explicit (unknown ≠ false/0).
	raw, err := json.Marshal(a.MotionVideo)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, mustNull := range []string{`"hasAudio":null`, `"frames":null`, `"loudnessLufs":null`} {
		if !strings.Contains(string(raw), mustNull) {
			t.Fatalf("fixture A wire block must contain %s, got %s", mustNull, raw)
		}
	}
	if strings.Contains(string(raw), `"hasAudio":false`) {
		t.Fatalf("fixture A wire block must never emit hasAudio=false for unknown: %s", raw)
	}
}

func TestMotionVideoDetailResolvesLatestAndProjects(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	processMotionVideoFixtures(t, agg)

	// Detail by root id.
	detail, err := agg.MetaAppDetail("22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0", "mvc")
	if err != nil || detail == nil {
		t.Fatalf("MetaAppDetail by root: %v %v", detail, err)
	}
	if detail.MotionVideo == nil || detail.MotionVideo.VideoSha256 != fixtureAVideoSha {
		t.Fatalf("detail motionVideo: %+v", detail.MotionVideo)
	}

	// A mid-chain modify folds: detail by the version pin id resolves the
	// same latest record and projects the updated manifest.
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "aaaa0001versionmodify:i0",
		Path:        PathMetaApp + "@22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0",
		Operation:   OperationModify,
		Timestamp:   3000,
		ContentType: "application/json",
		ContentBody: []byte(strings.Replace(fixtureAUkiyoeManifest, `"episode": 1`, `"episode": 2`, 1)),
	}))
	detailByVersion, err := agg.MetaAppDetail("aaaa0001versionmodify:i0", "mvc")
	if err != nil || detailByVersion == nil {
		t.Fatalf("MetaAppDetail by version pin: %v %v", detailByVersion, err)
	}
	if detailByVersion.PinID != "22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0" {
		t.Fatalf("detail by version must resolve the root, got %q", detailByVersion.PinID)
	}
	if detailByVersion.CurrentPinID != "aaaa0001versionmodify:i0" {
		t.Fatalf("detail currentPinId: %q", detailByVersion.CurrentPinID)
	}
	if detailByVersion.MotionVideo == nil || detailByVersion.MotionVideo.VideoSha256 != fixtureAVideoSha {
		t.Fatalf("detail by version motionVideo: %+v", detailByVersion.MotionVideo)
	}
}

func TestMotionVideoLearningStatusRule(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "missing sourceIncluded stays incomplete",
			payload: `{"title":"x","prompt":"p","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"` + fixtureAVideoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s"}}`,
			want:    LearningStatusIncomplete,
		},
		{
			name:    "empty prompt stays incomplete",
			payload: `{"title":"x","prompt":"   ","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"` + fixtureAVideoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
			want:    LearningStatusIncomplete,
		},
		{
			name:    "uppercase videoSha256 stays incomplete",
			payload: `{"title":"x","prompt":"p","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"B8113CCC77C592466F8C67B7FF775CB9C57927A516C34989E8611A1D09F81674","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
			want:    LearningStatusIncomplete,
		},
		{
			name:    "missing fps declaration stays incomplete",
			payload: `{"title":"x","prompt":"p","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"` + fixtureAVideoSha + `","durationSec":10,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
			want:    LearningStatusIncomplete,
		},
		{
			name:    "missing engine declaration stays incomplete",
			payload: `{"title":"x","prompt":"p","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"` + fixtureAVideoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","style":"s","sourceIncluded":true}}`,
			want:    LearningStatusIncomplete,
		},
		{
			name:    "full playback declarations plus source and prompt declared",
			payload: `{"title":"x","prompt":"p","tags":["motion-video"],"metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"` + fixtureAVideoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
			want:    LearningStatusDeclared,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agg, store := setupTestAggregator(t)
			defer store.Close()
			mustProcessMetaApp(t, agg, "learn-"+strings.ToLower(strings.ReplaceAll(tc.name, " ", "-"))+":i0", 1000, tc.payload)
			result, err := agg.ListMetaApps(MetaAppListParams{ChainName: "mvc"})
			if err != nil {
				t.Fatalf("ListMetaApps: %v", err)
			}
			if len(result.Items) != 1 || result.Items[0].MotionVideo == nil {
				t.Fatalf("motionVideo projection missing: %+v", result.Items)
			}
			if got := result.Items[0].MotionVideo.Learning.Status; got != tc.want {
				t.Fatalf("status: got %q want %q", got, tc.want)
			}
			if got := result.Items[0].MotionVideo.Learning.Status; got == "verified" {
				t.Fatal("verified must never be emitted in this milestone")
			}
		})
	}
}

func TestMotionVideoBlockAbsentForNonMotionVideoApps(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	processMotionVideoFixtures(t, agg)
	mustProcessMetaApp(t, agg, "plain-app:i0", 500, `{"title":"Plain","tags":["tool"]}`)

	result, err := agg.ListMetaApps(MetaAppListParams{ChainName: "mvc"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	for _, item := range result.Items {
		if item.PinID == "plain-app:i0" && item.MotionVideo != nil {
			t.Fatal("non-motion-video app must not carry a motionVideo block")
		}
	}
	if len(result.Items) != 3 {
		t.Fatalf("len: got %d want 3", len(result.Items))
	}
}

func TestMotionVideoDisabledExcludedButIncludeDisabledKeepsBlock(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	mustProcessMetaApp(t, agg, "disabled-mv:i0", 1000, `{"title":"Disabled MV","tags":["motion-video"],"disabled":true,"prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://v1i0","videoSha256":"`+fixtureAVideoSha+`","durationSec":10,"fps":24,"resolution":"r","engine":"e","style":"s","sourceIncluded":true}}`)

	result, err := agg.ListMetaApps(MetaAppListParams{ChainName: "mvc"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatal("disabled motion video must stay excluded from the default list")
	}
	result, err = agg.ListMetaApps(MetaAppListParams{ChainName: "mvc", IncludeDisabled: true})
	if err != nil {
		t.Fatalf("ListMetaApps includeDisabled: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].MotionVideo == nil {
		t.Fatalf("includeDisabled item keeps its motionVideo block: %+v", result.Items)
	}
}
