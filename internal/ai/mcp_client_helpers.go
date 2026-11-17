package ai

import "reflect"

// convertToInterfaceSlice converts any slice type to []interface{}
func convertToInterfaceSlice(slice interface{}) ([]interface{}, bool) {
	v := reflect.ValueOf(slice)

	// Check if it's a slice
	if v.Kind() != reflect.Slice {
		return nil, false
	}

	// Convert to []interface{}
	result := make([]interface{}, v.Len())
	for i := 0; i < v.Len(); i++ {
		result[i] = v.Index(i).Interface()
	}

	return result, true
}
