package logger

import "strings"

// PII (Personally Identifiable Information) masking helpers untuk audit log.
//
// Pattern: keep cukup info untuk debug/correlation tapi obscure detail.
// Apply di handler / dispatcher saat log structured field PII.
//
// Examples:
//   MaskEmail("aji@gmail.com")        → "a**@gmail.com"
//   MaskPhone("+6281234567890")       → "+62*****7890"
//   MaskPlate("B 1234 ABC")           → "B *** ABC"
//   MaskID("driver-postman-1")        → "driver-***"
//   MaskUUIDPrefix("550e8400-e29b-41d4-a716-446655440000") → "550e8400-..."

// MaskEmail — keep first char + domain visible.
//
//	"a@b.com" → "a@b.com" (too short, no mask)
//	"aji@gmail.com" → "a**@gmail.com"
//	"ajiperdana@gmail.com" → "a*********@gmail.com"
func MaskEmail(email string) string {
	if email == "" {
		return ""
	}
	at := strings.Index(email, "@")
	if at < 0 {
		return "***"
	}
	user := email[:at]
	domain := email[at:] // includes @
	if len(user) <= 2 {
		return email
	}
	return string(user[0]) + strings.Repeat("*", len(user)-1) + domain
}

// MaskPhone — keep prefix + last 4 digits.
//
//	"+6281234567890" → "+62*****7890"
//	"08123456" → "08***456" (short fallback)
func MaskPhone(phone string) string {
	if len(phone) < 8 {
		if len(phone) <= 3 {
			return phone
		}
		return phone[:2] + strings.Repeat("*", len(phone)-5) + phone[len(phone)-3:]
	}
	prefixLen := 3
	suffixLen := 4
	masked := strings.Repeat("*", len(phone)-prefixLen-suffixLen)
	return phone[:prefixLen] + masked + phone[len(phone)-suffixLen:]
}

// MaskPlate — Indonesian plate format "B 1234 ABC". Keep prefix + suffix area.
//
//	"B 1234 ABC" → "B *** ABC"
//	"AB 12 CD" → "AB ** CD"
func MaskPlate(plate string) string {
	parts := strings.Fields(plate)
	if len(parts) != 3 {
		// Unknown format → mask middle portion.
		if len(plate) <= 2 {
			return plate
		}
		mid := len(plate) - 2
		return string(plate[0]) + strings.Repeat("*", mid) + string(plate[len(plate)-1])
	}
	stars := strings.Repeat("*", len(parts[1]))
	return parts[0] + " " + stars + " " + parts[2]
}

// MaskID — driver_id atau opaque string identifier. Show first 8 char + asterisks.
//
//	"driver-postman-1" → "driver-p***"
//	"abc" → "***"
func MaskID(id string) string {
	const visible = 8
	if len(id) <= visible {
		return strings.Repeat("*", len(id))
	}
	return id[:visible] + "***"
}

// MaskUUIDPrefix — UUID format "550e8400-e29b-..." keep first segment.
//
//	"550e8400-e29b-41d4-a716-446655440000" → "550e8400-..."
//	Non-UUID → fallback ke MaskID
func MaskUUIDPrefix(s string) string {
	dash := strings.Index(s, "-")
	if dash < 0 {
		return MaskID(s)
	}
	return s[:dash] + "-..."
}
