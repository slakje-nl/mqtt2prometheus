package rules

import (
	"encoding/json"
	"fmt"
)

type Payload struct {
	raw      []byte
	document any
	parsed   bool
	err      error
}

func NewPayload(raw []byte) *Payload {
	return &Payload{raw: raw}
}

func (p *Payload) walk(path []string) (any, bool, error) {
	document, err := p.parse()
	if err != nil {
		return nil, false, err
	}

	for _, segment := range path {
		object, isObject := document.(map[string]any)
		if !isObject {
			return nil, false, nil
		}

		next, present := object[segment]
		if !present {
			return nil, false, nil
		}

		document = next
	}

	if document == nil {
		return nil, false, nil
	}

	return document, true, nil
}

func (p *Payload) parse() (any, error) {
	if !p.parsed {
		p.err = json.Unmarshal(p.raw, &p.document)
		p.parsed = true
	}

	if p.err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadJSON, p.err)
	}

	return p.document, nil
}
