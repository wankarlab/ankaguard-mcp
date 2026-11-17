package ai

import (
	"reflect"
	"time"
)

// extractCandleFromStruct extracts candle data from any struct type
func extractCandleFromStruct(data interface{}) Candle {
	candle := Candle{
		Timestamp: time.Now(),
	}

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return candle
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		switch field.Name {
		case "Timestamp", "Time":
			if value.Kind() == reflect.Struct && value.Type() == reflect.TypeOf(time.Time{}) {
				candle.Timestamp = value.Interface().(time.Time)
			}
		case "Open":
			candle.Open = getFloatFromValue(value)
		case "High":
			candle.High = getFloatFromValue(value)
		case "Low":
			candle.Low = getFloatFromValue(value)
		case "Close":
			candle.Close = getFloatFromValue(value)
		case "Volume":
			candle.Volume = getFloatFromValue(value)
		}
	}

	return candle
}

// getFloatFromValue extracts float64 from reflect.Value
func getFloatFromValue(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	default:
		return 0
	}
}
