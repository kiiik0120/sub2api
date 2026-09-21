package service

import (
	"strings"
	"time"
)

const volcengineCNYToUSD = 0.14

var (
	seedance25LaunchDiscountStart = time.Date(2026, time.August, 14, 14, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	seedance25LaunchDiscountEnd   = time.Date(2026, time.September, 17, 14, 0, 0, 0, time.FixedZone("CST", 8*60*60))
)

// ResolveSeedanceVideoOutputPricePerToken returns the official Ark online
// inference output-token price in USD. The caller must persist the returned
// value with the asynchronous task because resolution and source-video details
// are absent from a later status response.
func ResolveSeedanceVideoOutputPricePerToken(
	model, resolution string,
	inputContainsVideo, generateAudio bool,
	pricingAt time.Time,
) (float64, bool) {
	if pricingAt.IsZero() {
		pricingAt = time.Now()
	}

	resolution = normalizeSeedanceVideoResolution(resolution)
	priceCNYPerMTok, ok := seedanceVideoPriceCNYPerMTok(
		canonicalSeedanceVideoModel(model), resolution, inputContainsVideo, generateAudio, pricingAt,
	)
	if !ok {
		return 0, false
	}
	return priceCNYPerMTok * volcengineCNYToUSD * 1e-6, true
}

func normalizeSeedanceVideoResolution(resolution string) string {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "4k", "2160p":
		return "4k"
	default:
		return NormalizeVideoBillingResolutionOrDefault(resolution)
	}
}

func canonicalSeedanceVideoModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"volcengine/", "ark/"} {
		model = strings.TrimPrefix(model, prefix)
	}
	model = strings.NewReplacer(".", "-", "_", "-").Replace(model)

	switch {
	case model == "cdance2-0-0611", strings.HasPrefix(model, "doubao-seedance-2-0-fast"):
		if model == "cdance2-0-0611" {
			return "seedance-2-0"
		}
		return "seedance-2-0-fast"
	case strings.HasPrefix(model, "doubao-seedance-2-0-mini"):
		return "seedance-2-0-mini"
	case strings.HasPrefix(model, "doubao-seedance-2-5"):
		return "seedance-2-5"
	case strings.HasPrefix(model, "doubao-seedance-2-0"):
		return "seedance-2-0"
	case strings.HasPrefix(model, "doubao-seedance-1-5-pro"):
		return "seedance-1-5-pro"
	case strings.HasPrefix(model, "doubao-seedance-1-0-pro-fast"):
		return "seedance-1-0-pro-fast"
	case strings.HasPrefix(model, "doubao-seedance-1-0-pro"):
		return "seedance-1-0-pro"
	default:
		return ""
	}
}

func seedanceVideoPriceCNYPerMTok(model, resolution string, inputContainsVideo, generateAudio bool, pricingAt time.Time) (float64, bool) {
	switch model {
	case "seedance-2-5":
		switch resolution {
		case VideoBillingResolution480P, VideoBillingResolution720P:
			if inputContainsVideo {
				return 42, true
			}
			return 70, true
		case VideoBillingResolution1080P:
			price := 77.0
			if inputContainsVideo {
				price = 46
			}
			// The public 72% launch discount ran from 2026-08-14 14:00 to
			// 2026-09-17 14:00 CST.
			if !pricingAt.Before(seedance25LaunchDiscountStart) && pricingAt.Before(seedance25LaunchDiscountEnd) {
				price *= 0.72
			}
			return price, true
		}
	case "seedance-2-0":
		switch resolution {
		case VideoBillingResolution480P, VideoBillingResolution720P:
			if inputContainsVideo {
				return 28, true
			}
			return 46, true
		case VideoBillingResolution1080P:
			if inputContainsVideo {
				return 31, true
			}
			return 51, true
		case "4k":
			if inputContainsVideo {
				return 16, true
			}
			return 26, true
		}
	case "seedance-2-0-fast":
		// The documented promotion is enterprise-only and token-cap limited.
		// Neither condition is present in an async task, so use the list price;
		// a channel token-price override can represent an eligible account.
		if resolution == VideoBillingResolution480P || resolution == VideoBillingResolution720P {
			if inputContainsVideo {
				return 22, true
			}
			return 37, true
		}
	case "seedance-2-0-mini":
		// See the same enterprise-only promotion limitation as 2.0-fast above.
		if resolution == VideoBillingResolution480P || resolution == VideoBillingResolution720P {
			if inputContainsVideo {
				return 14, true
			}
			return 23, true
		}
	case "seedance-1-5-pro":
		if generateAudio {
			return 16, true
		}
		return 8, true
	case "seedance-1-0-pro":
		return 15, true
	case "seedance-1-0-pro-fast":
		return 4.2, true
	}
	return 0, false
}

// CalculateOutputTokenCost applies a known output-token rate to an asynchronous
// generation result. Input tokens are deliberately excluded: Ark video status
// reports only completion_tokens, which already represents the billable usage.
func CalculateOutputTokenCost(outputTokens int, outputPricePerToken, rateMultiplier float64) *CostBreakdown {
	if outputTokens < 0 {
		outputTokens = 0
	}
	if outputPricePerToken < 0 {
		outputPricePerToken = 0
	}
	if rateMultiplier < 0 {
		rateMultiplier = 0
	}
	outputCost := float64(outputTokens) * outputPricePerToken
	return &CostBreakdown{
		OutputCost:  outputCost,
		TotalCost:   outputCost,
		ActualCost:  outputCost * rateMultiplier,
		BillingMode: string(BillingModeToken),
	}
}
