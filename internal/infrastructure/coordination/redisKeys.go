package coordination

import "encoding/base64"

const redisNamespace = "houseflow:coordination:v1"

func encodeKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeKeyPart(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return string(decoded), err
}

func instanceHeartbeatKey(instanceID string) string {
	return redisNamespace + ":instance:" + encodeKeyPart(instanceID)
}

func roomLeaseKey(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":lease"
}

func roomFenceKey(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":fence"
}

func roomEventChannel(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":events"
}

func commandStreamKey(instanceID string) string {
	return redisNamespace + ":instance:" + encodeKeyPart(instanceID) + ":commands"
}

func deduplicationKey(scopeID string, messageID string) string {
	return redisNamespace + ":dedup:" + encodeKeyPart(scopeID) + ":" + encodeKeyPart(messageID)
}

func presenceHashKey(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":presence:data"
}

func presenceExpiryKey(roomID string) string {
	return redisNamespace + ":room:" + encodeKeyPart(roomID) + ":presence:expiry"
}

func presenceMemberID(instanceID string, connectionID string) string {
	return encodeKeyPart(instanceID) + ":" + encodeKeyPart(connectionID)
}
