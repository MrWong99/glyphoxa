package entity

import (
	"errors"
	"fmt"
)

// Validate checks an [EntityDefinition] for required fields and valid types.
//
// Rules:
//   - Name must be non-empty.
//   - Type must be a recognised [EntityType].
//   - Every [RelationshipDef] must have a non-empty Type.
func Validate(entity EntityDefinition) error {
	var errs []error

	if entity.Name == "" {
		errs = append(errs, errors.New("name must not be empty"))
	}

	if !entity.Type.IsValid() {
		errs = append(errs, fmt.Errorf("type %q is not a recognised entity type", entity.Type))
	}

	for i, rel := range entity.Relationships {
		if rel.Type == "" {
			errs = append(errs, fmt.Errorf("relationship[%d]: type must not be empty", i))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
