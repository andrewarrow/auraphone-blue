package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
)

// ============================================================================
// AdvertiseCallback Implementation (implements kotlin.AdvertiseCallback interface)
// ============================================================================

func (a *Android) OnStartSuccess(settingsInEffect *kotlin.AdvertiseSettings) {
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📡 Advertising started successfully")
}

func (a *Android) OnStartFailure(errorCode int) {
	var errorMsg string
	switch errorCode {
	case kotlin.ADVERTISE_FAILED_DATA_TOO_LARGE:
		errorMsg = "data too large"
	case kotlin.ADVERTISE_FAILED_TOO_MANY_ADVERTISERS:
		errorMsg = "too many advertisers"
	case kotlin.ADVERTISE_FAILED_ALREADY_STARTED:
		errorMsg = "already started"
	case kotlin.ADVERTISE_FAILED_INTERNAL_ERROR:
		errorMsg = "internal error"
	case kotlin.ADVERTISE_FAILED_FEATURE_UNSUPPORTED:
		errorMsg = "feature unsupported"
	default:
		errorMsg = fmt.Sprintf("unknown error %d", errorCode)
	}

	logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Advertising failed: %s", errorMsg)
}
