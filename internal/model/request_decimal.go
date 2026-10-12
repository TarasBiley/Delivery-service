package model

import (
	"encoding/json"
	"errors"
)

// RequestDecimal preserves a JSON number token for exact decimal validation and
// hashing. Numeric strings stay invalid, preserving the existing HTTP contract.
type RequestDecimal string

// UnmarshalJSON accepts number/null tokens, leaving required/range checks to validator.
func (n *RequestDecimal) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return errors.New("decimal field must be a JSON number")
	}
	var value json.Number
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*n = RequestDecimal(value.String())
	return nil
}

// MarshalJSON keeps the request's wire representation numeric.
func (n RequestDecimal) MarshalJSON() ([]byte, error) { return json.Marshal(json.Number(n)) }

// String returns the original decimal token before normalization.
func (n RequestDecimal) String() string { return string(n) }
