package httpapi

import (
	"qingye/backend/internal/domain/legacycatalog"
	"qingye/backend/internal/maintenance"
)

type legacyCategory = legacycatalog.LegacyCategory

var legacyCategories = legacycatalog.LegacyCategories
var confirmedLegacyPrice = legacycatalog.ConfirmedLegacyPrice
var deterministicID = legacycatalog.DeterministicID
var legacyMediaLocation = legacycatalog.LegacyMediaLocation
var legacyImageURLs = legacycatalog.LegacyImageURLs
var regexpLegacyKey = maintenance.RegexpLegacyKey
