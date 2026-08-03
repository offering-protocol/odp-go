package odp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func decodeAdditional(data []byte, known any) (AdditionalMembers, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, known); err != nil {
		return nil, err
	}
	valueType := reflect.TypeOf(known)
	if valueType.Kind() != reflect.Pointer || valueType.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("ODP additional-member target must be a struct pointer")
	}
	for name := range jsonFieldNames(valueType.Elem()) {
		delete(object, name)
	}
	if len(object) == 0 {
		return nil, nil
	}
	return object, nil
}

func encodeAdditional(known any, additional AdditionalMembers) ([]byte, error) {
	data, err := json.Marshal(known)
	if err != nil {
		return nil, err
	}
	if len(additional) == 0 {
		return data, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	knownType := reflect.TypeOf(known)
	if knownType.Kind() == reflect.Pointer {
		knownType = knownType.Elem()
	}
	fields := jsonFieldNames(knownType)
	for name, value := range additional {
		if _, knownName := fields[name]; !knownName {
			object[name] = value
		}
	}
	return json.Marshal(object)
}

func jsonFieldNames(valueType reflect.Type) map[string]struct{} {
	names := make(map[string]struct{}, valueType.NumField())
	for index := 0; index < valueType.NumField(); index++ {
		name := strings.Split(valueType.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names[name] = struct{}{}
		}
	}
	return names
}

func (value *ServiceDocument) UnmarshalJSON(data []byte) error {
	type plain ServiceDocument
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = ServiceDocument(decoded)
	value.Additional = additional
	return nil
}

func (value ServiceDocument) MarshalJSON() ([]byte, error) {
	type plain ServiceDocument
	return encodeAdditional(plain(value), value.Additional)
}

func (value *Collection) UnmarshalJSON(data []byte) error {
	type plain Collection
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = Collection(decoded)
	value.Additional = additional
	return nil
}

func (value Collection) MarshalJSON() ([]byte, error) {
	type plain Collection
	return encodeAdditional(plain(value), value.Additional)
}

func (value *Offering) UnmarshalJSON(data []byte) error {
	type plain Offering
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = Offering(decoded)
	value.Additional = additional
	return nil
}

func (value Offering) MarshalJSON() ([]byte, error) {
	type plain Offering
	return encodeAdditional(plain(value), value.Additional)
}

func (value *ProblemDetails) UnmarshalJSON(data []byte) error {
	type plain ProblemDetails
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = ProblemDetails(decoded)
	value.Additional = additional
	return nil
}

func (value ProblemDetails) MarshalJSON() ([]byte, error) {
	type plain ProblemDetails
	return encodeAdditional(plain(value), value.Additional)
}

func (value *FilterDefinition) UnmarshalJSON(data []byte) error {
	type plain FilterDefinition
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = FilterDefinition(decoded)
	value.Additional = additional
	return nil
}

func (value FilterDefinition) MarshalJSON() ([]byte, error) {
	type plain FilterDefinition
	return encodeAdditional(plain(value), value.Additional)
}

func (value *SortDefinition) UnmarshalJSON(data []byte) error {
	type plain SortDefinition
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = SortDefinition(decoded)
	value.Additional = additional
	return nil
}

func (value SortDefinition) MarshalJSON() ([]byte, error) {
	type plain SortDefinition
	return encodeAdditional(plain(value), value.Additional)
}

func (value *PricePreview) UnmarshalJSON(data []byte) error {
	type plain PricePreview
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = PricePreview(decoded)
	value.Additional = additional
	return nil
}

func (value PricePreview) MarshalJSON() ([]byte, error) {
	type plain PricePreview
	return encodeAdditional(plain(value), value.Additional)
}

func (value *SortKey) UnmarshalJSON(data []byte) error {
	type plain SortKey
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = SortKey(decoded)
	value.Additional = additional
	return nil
}

func (value SortKey) MarshalJSON() ([]byte, error) {
	type plain SortKey
	return encodeAdditional(plain(value), value.Additional)
}

func (value *SearchCapabilities) UnmarshalJSON(data []byte) error {
	type plain SearchCapabilities
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = SearchCapabilities(decoded)
	value.Additional = additional
	return nil
}

func (value SearchCapabilities) MarshalJSON() ([]byte, error) {
	type plain SearchCapabilities
	return encodeAdditional(plain(value), value.Additional)
}

func (value *InvalidParameter) UnmarshalJSON(data []byte) error {
	type plain InvalidParameter
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = InvalidParameter(decoded)
	value.Additional = additional
	return nil
}

func (value InvalidParameter) MarshalJSON() ([]byte, error) {
	type plain InvalidParameter
	return encodeAdditional(plain(value), value.Additional)
}

func (value *FilterExpression) UnmarshalJSON(data []byte) error {
	type plain FilterExpression
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = FilterExpression(decoded)
	value.Additional = additional
	return nil
}

func (value FilterExpression) MarshalJSON() ([]byte, error) {
	type plain FilterExpression
	return encodeAdditional(plain(value), value.Additional)
}

func (value *OfferingSearchRequest) UnmarshalJSON(data []byte) error {
	type plain OfferingSearchRequest
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = OfferingSearchRequest(decoded)
	value.Additional = additional
	return nil
}

func (value OfferingSearchRequest) MarshalJSON() ([]byte, error) {
	type plain OfferingSearchRequest
	return encodeAdditional(plain(value), value.Additional)
}

func (value *Page[Item]) UnmarshalJSON(data []byte) error {
	type plain Page[Item]
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = Page[Item](decoded)
	value.Additional = additional
	return nil
}

func (value Page[Item]) MarshalJSON() ([]byte, error) {
	type plain Page[Item]
	return encodeAdditional(plain(value), value.Additional)
}

func (value *OfferingPage[Item]) UnmarshalJSON(data []byte) error {
	type plain OfferingPage[Item]
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = OfferingPage[Item](decoded)
	value.Additional = additional
	return nil
}

func (value OfferingPage[Item]) MarshalJSON() ([]byte, error) {
	type plain OfferingPage[Item]
	return encodeAdditional(plain(value), value.Additional)
}

func (value *HTTPConfiguration) UnmarshalJSON(data []byte) error {
	type plain HTTPConfiguration
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = HTTPConfiguration(decoded)
	value.Additional = additional
	return nil
}
func (value HTTPConfiguration) MarshalJSON() ([]byte, error) {
	type plain HTTPConfiguration
	return encodeAdditional(plain(value), value.Additional)
}

func (value *CapabilityLink) UnmarshalJSON(data []byte) error {
	type plain CapabilityLink
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = CapabilityLink(decoded)
	value.Additional = additional
	return nil
}
func (value CapabilityLink) MarshalJSON() ([]byte, error) {
	type plain CapabilityLink
	return encodeAdditional(plain(value), value.Additional)
}

func (value *FilterUnit) UnmarshalJSON(data []byte) error {
	type plain FilterUnit
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = FilterUnit(decoded)
	value.Additional = additional
	return nil
}
func (value FilterUnit) MarshalJSON() ([]byte, error) {
	type plain FilterUnit
	return encodeAdditional(plain(value), value.Additional)
}

func (value *FilterCapabilitySource) UnmarshalJSON(data []byte) error {
	type plain FilterCapabilitySource
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = FilterCapabilitySource(decoded)
	value.Additional = additional
	return nil
}
func (value FilterCapabilitySource) MarshalJSON() ([]byte, error) {
	type plain FilterCapabilitySource
	return encodeAdditional(plain(value), value.Additional)
}

func (value *SortCapabilitySource) UnmarshalJSON(data []byte) error {
	type plain SortCapabilitySource
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = SortCapabilitySource(decoded)
	value.Additional = additional
	return nil
}
func (value SortCapabilitySource) MarshalJSON() ([]byte, error) {
	type plain SortCapabilitySource
	return encodeAdditional(plain(value), value.Additional)
}

func (value *CollectionSearchRequest) UnmarshalJSON(data []byte) error {
	type plain CollectionSearchRequest
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = CollectionSearchRequest(decoded)
	value.Additional = additional
	return nil
}
func (value CollectionSearchRequest) MarshalJSON() ([]byte, error) {
	type plain CollectionSearchRequest
	return encodeAdditional(plain(value), value.Additional)
}

func (value *RefinementBucket) UnmarshalJSON(data []byte) error {
	type plain RefinementBucket
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = RefinementBucket(decoded)
	value.Additional = additional
	return nil
}
func (value RefinementBucket) MarshalJSON() ([]byte, error) {
	type plain RefinementBucket
	return encodeAdditional(plain(value), value.Additional)
}

func (value *RefinementGroup) UnmarshalJSON(data []byte) error {
	type plain RefinementGroup
	var decoded plain
	additional, err := decodeAdditional(data, &decoded)
	if err != nil {
		return err
	}
	*value = RefinementGroup(decoded)
	value.Additional = additional
	return nil
}
func (value RefinementGroup) MarshalJSON() ([]byte, error) {
	type plain RefinementGroup
	return encodeAdditional(plain(value), value.Additional)
}
