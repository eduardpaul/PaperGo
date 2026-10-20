package catalog

// FieldType is what a field holds. Field values carry the same types.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldNote     FieldType = "note"
	FieldEmail    FieldType = "email"
	FieldURL      FieldType = "url"
	FieldDate     FieldType = "date"
	FieldLookup   FieldType = "lookup"
	FieldPerson   FieldType = "person"
	FieldTerm     FieldType = "term"
	FieldKeywords FieldType = "keywords"
	FieldChoice   FieldType = "choice"
	FieldDatetime FieldType = "datetime"
	FieldInteger  FieldType = "integer"
	FieldDecimal  FieldType = "decimal"
	FieldNumber   FieldType = "number"
	FieldCurrency FieldType = "currency"
	FieldBoolean  FieldType = "boolean"
)

// FieldTypes lists every field type.
var FieldTypes = []FieldType{FieldText, FieldNote, FieldEmail, FieldURL, FieldDate, FieldLookup, FieldPerson, FieldTerm,
	FieldKeywords, FieldChoice, FieldDatetime, FieldInteger, FieldDecimal, FieldNumber, FieldCurrency, FieldBoolean}

func (t FieldType) Valid() bool {
	for _, v := range FieldTypes {
		if t == v {
			return true
		}
	}
	return false
}

// Multivalued reports whether a field of the type may hold several values.
func (t FieldType) Multivalued() bool {
	switch t {
	case FieldChoice, FieldPerson, FieldLookup, FieldTerm, FieldKeywords:
		return true
	}
	return false
}

// Terms reports whether the type holds term ids.
func (t FieldType) Terms() bool { return t == FieldTerm || t == FieldKeywords }

// IndexStatus is whether a field's index is usable.
type IndexStatus string

const (
	IndexReady    IndexStatus = "ready"
	IndexBuilding IndexStatus = "building"
	IndexFailed   IndexStatus = "failed"
)

func (s IndexStatus) Valid() bool { return s == IndexReady || s == IndexBuilding || s == IndexFailed }
