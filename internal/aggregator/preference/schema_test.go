package preference

import (
	"fmt"
	"strings"
	"testing"
)

func refA(appRoot, version, video, sha string) string {
	return fmt.Sprintf(`{"chainName":"mvc","app":"metaapp://%s","version":"metaapp://%s","video":"metafile://%s","videoSha256":"%s"}`, appRoot, version, video, sha)
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func batchJSON(categories string, items ...string) string {
	return fmt.Sprintf(`{"category":[%s],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0","mode":"arena"},"items":[%s]}`, categories, strings.Join(items, ","))
}

func itemJSON(refAJSON, refBJSON, pick string) string {
	return fmt.Sprintf(`{"a":%s,"b":%s,"pick":%q}`, refAJSON, refBJSON, pick)
}

func TestParseComparisonBatchValidBounds(t *testing.T) {
	// One-item batch parses.
	item := itemJSON(refA("app-a:i0", "app-a:i0", "video-a:i0", shaA), refA("app-b:i0", "app-b:i0", "video-b:i0", shaB), "a")
	_, _, items, err := parseComparisonBatch([]byte(batchJSON(`"motion-video"`, item)))
	if err != nil {
		t.Fatalf("one-item batch: %v", err)
	}
	if len(items) != 1 || items[0].Pick != "a" {
		t.Fatalf("items: %+v", items)
	}

	// 28-item batch parses; 29 rejected (§8 vector 1).
	items28 := make([]string, 0, 29)
	for i := 0; i < 29; i++ {
		pick := "a"
		if i%2 == 1 {
			pick = "b"
		}
		items28 = append(items28, itemJSON(
			refA("app-a:i0", "app-a:i0", fmt.Sprintf("video-a%d:i0", i), shaA),
			refA("app-b:i0", "app-b:i0", fmt.Sprintf("video-b%d:i0", i), shaB),
			pick))
	}
	if _, _, _, err := parseComparisonBatch([]byte(batchJSON(`"motion-video"`, items28[:28]...))); err != nil {
		t.Fatalf("28-item batch: %v", err)
	}
	if _, _, _, err := parseComparisonBatch([]byte(batchJSON(`"motion-video"`, items28...))); err == nil {
		t.Fatal("29-item batch must be rejected")
	}
}

func TestParseComparisonBatchRejectsMalformed(t *testing.T) {
	goodA := refA("app-a:i0", "app-a:i0", "video-a:i0", shaA)
	goodB := refA("app-b:i0", "app-b:i0", "video-b:i0", shaB)

	cases := []struct {
		name   string
		body   string
		errMsg string
	}{
		{"missing category", `{"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a"}]}`, "category"},
		{"bad kind", `{"category":["motion-video"],"kind":"reaction-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a"}]}`, "kind"},
		{"missing client", `{"category":["motion-video"],"kind":"comparison-batch","items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a"}]}`, "client.artifact"},
		{"invalid tag regex", batchJSON(`"Motion_Video"`, itemJSON(goodA, goodB, "a")), "tag pattern"},
		{"nine categories", batchJSON(`"a","b","c","d","e","f","g","h","i"`, itemJSON(goodA, goodB, "a")), "1-8"},
		{"empty items", batchJSON(`"motion-video"`), "items"},
		{"missing pick", `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `}]}`, "pick"},
		{"bad pick", batchJSON(`"motion-video"`, itemJSON(goodA, goodB, "c")), "pick"},
		{"same video pair", batchJSON(`"motion-video"`, itemJSON(refA("app-a:i0", "app-a:i0", "video-a:i0", shaA), refA("app-a:i0", "app-a:i0", "video-a2:i0", shaA), "a")), "identical videoSha256"},
		{"cross-chain pair", batchJSON(`"motion-video"`, itemJSON(goodA, `{"chainName":"btc","app":"metaapp://app-b:i0","version":"metaapp://app-b:i0","video":"metafile://video-b:i0","videoSha256":"`+shaB+`"}`, "a")), "chainName"},
		{"uppercase sha", batchJSON(`"motion-video"`, itemJSON(refA("app-a:i0", "app-a:i0", "video-a:i0", strings.ToUpper(shaA)), goodB, "a")), "videoSha256"},
		{"non-metafile video", batchJSON(`"motion-video"`, itemJSON(`{"chainName":"mvc","app":"metaapp://app-a:i0","version":"metaapp://app-a:i0","video":"https://x/v.mp4","videoSha256":"`+shaA+`"}`, goodB, "a")), "video"},
		{"bad dims id", `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a","dims":{"1visual":"a"}}]}`, "dimension pattern"},
		{"bad dims value", `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a","dims":{"visual":"A"}}]}`, "dims"},
		{"seven dims", `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a","dims":{"d1":"a","d2":"a","d3":"a","d4":"a","d5":"a","d6":"a","d7":"a"}}]}`, "dims"},
		{"reason too long", `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":` + goodA + `,"b":` + goodB + `,"pick":"a","reason":"` + strings.Repeat("r", 1001) + `"}]}`, "reason"},
		{"non-json payload", `not json`, "JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseComparisonBatch([]byte(tc.body))
			if err == nil {
				t.Fatal("batch must be rejected")
			}
			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("error %q must mention %q", err.Error(), tc.errMsg)
			}
		})
	}
}

func TestParseComparisonBatchOneBadItemInvalidatesWholePin(t *testing.T) {
	goodA := refA("app-a:i0", "app-a:i0", "video-a:i0", shaA)
	goodB := refA("app-b:i0", "app-b:i0", "video-b:i0", shaB)
	badItem := `{"a":` + goodA + `,"b":` + goodA + `,"pick":"a"}` // same-video pair

	body := batchJSON(`"motion-video"`, itemJSON(goodA, goodB, "tie"), badItem)
	if _, _, _, err := parseComparisonBatch([]byte(body)); err == nil {
		t.Fatal("one malformed item must invalidate the whole pin")
	}
}

func TestCategoryRules(t *testing.T) {
	if !hasMotionVideoCategory([]string{"other", "motion-video"}) {
		t.Fatal("motion-video category must be detected")
	}
	if hasMotionVideoCategory([]string{"motion-videox"}) {
		t.Fatal("prefix match is not category membership")
	}
	if !isTestBatch([]string{"motion-video", "test:motion-video-smoke"}) {
		t.Fatal("test: tag must mark smoke evidence")
	}
	if isTestBatch([]string{"motion-video", "contest"}) {
		t.Fatal("non test: tag must not mark smoke evidence")
	}
}
