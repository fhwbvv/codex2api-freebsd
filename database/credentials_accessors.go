package database

import "strconv"

func (a *AccountRow) GetCredentialFloat64(key string) (float64, bool) {
	if a == nil || a.Credentials == nil {
		return 0, false
	}
	v, ok := a.Credentials[key]
	if !ok || v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		parsed, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func (a *AccountRow) GetCredentialBool(key string) bool {
	if a == nil || a.Credentials == nil {
		return false
	}
	v, ok := a.Credentials[key]
	if !ok || v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		parsed, err := strconv.ParseBool(val)
		return err == nil && parsed
	default:
		return false
	}
}
