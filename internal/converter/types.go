package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alecthomas/jsonschema"
	"github.com/envoyproxy/protoc-gen-validate/validate"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/xeipuuv/gojsonschema"
)

var (
	globalPkg = &ProtoPackage{
		name:     "",
		parent:   nil,
		children: make(map[string]*ProtoPackage),
		types:    make(map[string]*descriptor.DescriptorProto),
	}
)

func (c *Converter) registerType(pkgName *string, msg *descriptor.DescriptorProto) {
	pkg := globalPkg
	if pkgName != nil {
		for _, node := range strings.Split(*pkgName, ".") {
			if pkg == globalPkg && node == "" {
				// Skips leading "."
				continue
			}
			child, ok := pkg.children[node]
			if !ok {
				child = &ProtoPackage{
					name:     pkg.name + "." + node,
					parent:   pkg,
					children: make(map[string]*ProtoPackage),
					types:    make(map[string]*descriptor.DescriptorProto),
				}
				pkg.children[node] = child
			}
			pkg = child
		}
	}
	pkg.types[msg.GetName()] = msg
}

func (c *Converter) relativelyLookupNestedType(desc *descriptor.DescriptorProto, name string) (*descriptor.DescriptorProto, bool) {
	components := strings.Split(name, ".")
componentLoop:
	for _, component := range components {
		for _, nested := range desc.GetNestedType() {
			if nested.GetName() == component {
				desc = nested
				continue componentLoop
			}
		}
		c.logger.WithField("component", component).WithField("description", desc.GetName()).Info("no such nested message")
		return nil, false
	}
	return desc, true
}

// Convert a proto "field" (essentially a type-switch with some recursion):
func (c *Converter) convertField(curPkg *ProtoPackage, desc *descriptor.FieldDescriptorProto, msg *descriptor.DescriptorProto) (*jsonschema.Type, bool, error) {
	var validationRules *validate.FieldRules = nil
	required := false
	if desc.Options != nil {
		if ext, err := proto.GetExtension(desc.Options, validate.E_Rules); err == proto.ErrMissingExtension {

		} else if err != nil {
			return nil, required, err
		} else if rule, ok := ext.(*validate.FieldRules); ok {
			validationRules = rule
		}
	}

	if validationRules != nil && validationRules.Message != nil && validationRules.Message.Required != nil {
		required = *validationRules.Message.Required
	}

	// Prepare a new jsonschema.Type for our eventual return value:
	jsonSchemaType := &jsonschema.Type{
		Properties: make(map[string]*jsonschema.Type),
	}

	// Generate a description from src comments (if available)
	if src := c.sourceInfo.GetField(desc); src != nil {
		jsonSchemaType.Description = formatDescription(src)
	}

	// Switch the types, and pick a JSONSchema equivalent:
	switch desc.GetType() {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE,
		descriptor.FieldDescriptorProto_TYPE_FLOAT:
		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: gojsonschema.TYPE_NUMBER},
			}
		} else {
			jsonSchemaType.Type = gojsonschema.TYPE_NUMBER
		}

	case descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_UINT32,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED32,
		descriptor.FieldDescriptorProto_TYPE_SINT32:
		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: gojsonschema.TYPE_INTEGER},
			}
		} else {
			jsonSchemaType.Type = gojsonschema.TYPE_INTEGER
		}

	case descriptor.FieldDescriptorProto_TYPE_INT64,
		descriptor.FieldDescriptorProto_TYPE_UINT64,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_SFIXED64,
		descriptor.FieldDescriptorProto_TYPE_SINT64:
		jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_INTEGER})
		if !c.DisallowBigIntsAsStrings {
			jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_STRING})
		}
		if c.AllowNullValues {
			jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_NULL})
		}

	case descriptor.FieldDescriptorProto_TYPE_STRING,
		descriptor.FieldDescriptorProto_TYPE_BYTES:
		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: gojsonschema.TYPE_STRING},
			}
		} else {
			jsonSchemaType.Type = gojsonschema.TYPE_STRING
			if stringRules := validationRules.GetString_(); stringRules != nil {
				if constValue := stringRules.GetConst(); constValue != "" {
					jsonSchemaType.Enum = []interface{}{constValue}
				}
				if min := stringRules.GetMinLen(); min != 0 {
					jsonSchemaType.MinLength = int(min)
				}
				if max := stringRules.GetMaxLen(); max != 0 {
					jsonSchemaType.MaxLength = int(max)
				}
				if len := stringRules.GetLen(); len > 0 {
					jsonSchemaType.MinLength = int(len)
					jsonSchemaType.MaxLength = int(len)
				}
				if pattern := stringRules.GetPattern(); pattern != "" {
					jsonSchemaType.Pattern = pattern
				}
				//Prefix and suffix implemented via regexp.
				//This means that you can't use prefix, suffix and regexp at the same time.
				if prefix := stringRules.GetPrefix(); prefix != "" {
					jsonSchemaType.Pattern = fmt.Sprintf("^%s.*", prefix)
				}
				if suffix := stringRules.GetSuffix(); suffix != "" {
					jsonSchemaType.Pattern = fmt.Sprintf(".*%s$", suffix)
				}
				if in := stringRules.GetIn(); in != nil {
					values := make([]interface{}, len(in))
					for i := range in {
						values[i] = in[i]
					}
					jsonSchemaType.Enum = values
				}
				if notIn := stringRules.GetNotIn(); notIn != nil {
					values := make([]interface{}, len(notIn))
					for i := range notIn {
						values[i] = notIn[i]
					}
					jsonSchemaType.Not = &jsonschema.Type{
						Enum: values,
					}
				}
				if email := stringRules.GetEmail(); email {
					jsonSchemaType.Format = "email"
				}
				if address := stringRules.GetAddress(); address {
					jsonSchemaType.Type = ""
					jsonSchemaType.OneOf = []*jsonschema.Type{
						&jsonschema.Type{Type: gojsonschema.TYPE_STRING, Format: "ipv4"},
						&jsonschema.Type{Type: gojsonschema.TYPE_STRING, Format: "ipv6"},
						&jsonschema.Type{Type: gojsonschema.TYPE_STRING, Format: "hostname"},
					}
				}
				if hostname := stringRules.GetHostname(); hostname {
					jsonSchemaType.Format = "hostname"
				}
				if ip := stringRules.GetIp(); ip {
					jsonSchemaType.Type = ""
					jsonSchemaType.OneOf = []*jsonschema.Type{
						&jsonschema.Type{Type: gojsonschema.TYPE_STRING, Format: "ipv4"},
						&jsonschema.Type{Type: gojsonschema.TYPE_STRING, Format: "ipv6"},
					}
				}
				if ipv4 := stringRules.GetIpv4(); ipv4 {
					jsonSchemaType.Format = "ipv4"
				}
				if ipv6 := stringRules.GetIpv6(); ipv6 {
					jsonSchemaType.Format = "ipv6"
				}
				if uri := stringRules.GetUri(); uri {
					jsonSchemaType.Format = "uri"
				}
				if uriRef := stringRules.GetUriRef(); uriRef {
					jsonSchemaType.Format = "uri-reference"
				}
				if uuid := stringRules.GetUuid(); uuid {
					jsonSchemaType.Format = "uuid"
				}
			}
		}

	case descriptor.FieldDescriptorProto_TYPE_ENUM:
		jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_STRING})
		jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_INTEGER})
		if c.AllowNullValues {
			jsonSchemaType.OneOf = append(jsonSchemaType.OneOf, &jsonschema.Type{Type: gojsonschema.TYPE_NULL})
		}

		// Go through all the enums we have, see if we can match any to this field by name:
		for _, enumDescriptor := range msg.GetEnumType() {

			// Each one has several values:
			for _, enumValue := range enumDescriptor.Value {

				// Figure out the entire name of this field:
				fullFieldName := fmt.Sprintf(".%v.%v", *msg.Name, *enumDescriptor.Name)

				// If we find ENUM values for this field then put them into the JSONSchema list of allowed ENUM values:
				if strings.HasSuffix(desc.GetTypeName(), fullFieldName) {
					jsonSchemaType.Enum = append(jsonSchemaType.Enum, enumValue.Name)
					jsonSchemaType.Enum = append(jsonSchemaType.Enum, enumValue.Number)
				}
			}
		}

	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: gojsonschema.TYPE_BOOLEAN},
			}
		} else {
			jsonSchemaType.Type = gojsonschema.TYPE_BOOLEAN
		}

	case descriptor.FieldDescriptorProto_TYPE_GROUP,
		descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		jsonSchemaType.Type = gojsonschema.TYPE_OBJECT
		if desc.GetLabel() == descriptor.FieldDescriptorProto_LABEL_OPTIONAL {
			jsonSchemaType.AdditionalProperties = []byte("true")
		}
		if desc.GetLabel() == descriptor.FieldDescriptorProto_LABEL_REQUIRED {
			jsonSchemaType.AdditionalProperties = []byte("false")
		}

	default:
		return nil, required, fmt.Errorf("unrecognized field type: %s", desc.GetType().String())
	}

	// Recurse array of primitive types:
	if desc.GetLabel() == descriptor.FieldDescriptorProto_LABEL_REPEATED && jsonSchemaType.Type != gojsonschema.TYPE_OBJECT {
		jsonSchemaType.Items = &jsonschema.Type{}

		if len(jsonSchemaType.Enum) > 0 {
			jsonSchemaType.Items.Enum = jsonSchemaType.Enum
			jsonSchemaType.Enum = nil
			jsonSchemaType.Items.OneOf = nil
		} else {
			jsonSchemaType.Items.Type = jsonSchemaType.Type
			jsonSchemaType.Items.OneOf = jsonSchemaType.OneOf
		}

		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: gojsonschema.TYPE_ARRAY},
			}
		} else {
			jsonSchemaType.Type = gojsonschema.TYPE_ARRAY
			jsonSchemaType.OneOf = []*jsonschema.Type{}
		}

		return jsonSchemaType, required, nil
	}

	// Recurse nested objects / arrays of objects (if necessary):
	if jsonSchemaType.Type == gojsonschema.TYPE_OBJECT {

		recordType, ok := c.lookupType(curPkg, desc.GetTypeName())
		if !ok {
			return nil, required, fmt.Errorf("no such message type named %s", desc.GetTypeName())
		}

		// Recurse the recordType:
		recursedJSONSchemaType, err := c.convertMessageType(curPkg, recordType)
		if err != nil {
			return nil, required, err
		}

		// Maps, arrays, and objects are structured in different ways:
		switch {

		// Maps:
		case recordType.Options.GetMapEntry():
			c.logger.
				WithField("field_name", recordType.GetName()).
				WithField("msg_name", *msg.Name).
				Tracef("Is a map")

			// Make sure we have a "value":
			if _, ok := recursedJSONSchemaType.Properties["value"]; !ok {
				return nil, required, fmt.Errorf("Unable to find 'value' property of MAP type")
			}

			// Marshal the "value" properties to JSON (because that's how we can pass on AdditionalProperties):
			additionalPropertiesJSON, err := json.Marshal(recursedJSONSchemaType.Properties["value"])
			if err != nil {
				return nil, required, err
			}
			jsonSchemaType.AdditionalProperties = additionalPropertiesJSON

		// Arrays:
		case desc.GetLabel() == descriptor.FieldDescriptorProto_LABEL_REPEATED:
			jsonSchemaType.Items = &recursedJSONSchemaType
			jsonSchemaType.Type = gojsonschema.TYPE_ARRAY

		// Objects:
		default:
			jsonSchemaType.Properties = recursedJSONSchemaType.Properties
		}

		// Optionally allow NULL values:
		if c.AllowNullValues {
			jsonSchemaType.OneOf = []*jsonschema.Type{
				{Type: gojsonschema.TYPE_NULL},
				{Type: jsonSchemaType.Type},
			}
			jsonSchemaType.Type = ""
		}
	}

	return jsonSchemaType, required, nil
}

// Converts a proto "MESSAGE" into a JSON-Schema:
func (c *Converter) convertMessageType(curPkg *ProtoPackage, msg *descriptor.DescriptorProto) (jsonschema.Type, error) {

	// Prepare a new jsonschema:
	jsonSchemaType := jsonschema.Type{
		Properties: make(map[string]*jsonschema.Type),
		Version:    "http://json-schema.org/draft-07/schema#",
	}
	// Generate a description from src comments (if available)
	if src := c.sourceInfo.GetMessage(msg); src != nil {
		jsonSchemaType.Description = formatDescription(src)
	}

	// Optionally allow NULL values:
	if c.AllowNullValues {
		jsonSchemaType.OneOf = []*jsonschema.Type{
			{Type: gojsonschema.TYPE_NULL},
			{Type: gojsonschema.TYPE_OBJECT},
		}
	} else {
		jsonSchemaType.Type = gojsonschema.TYPE_OBJECT
	}

	// disallowAdditionalProperties will prevent validation where extra fields are found (outside of the schema):
	if c.DisallowAdditionalProperties {
		jsonSchemaType.AdditionalProperties = []byte("false")
	} else {
		jsonSchemaType.AdditionalProperties = []byte("true")
	}

	c.logger.WithField("message_str", proto.MarshalTextString(msg)).Trace("Converting message")
	for _, fieldDesc := range msg.GetField() {
		recursedJSONSchemaType, required, err := c.convertField(curPkg, fieldDesc, msg)
		c.logger.WithField("field_name", fieldDesc.GetName()).WithField("type", recursedJSONSchemaType.Type).Debug("Converted field")
		if err != nil {
			c.logger.WithError(err).WithField("field_name", fieldDesc.GetName()).WithField("message_name", msg.GetName()).Error("Failed to convert field")
			return jsonSchemaType, err
		}
		jsonSchemaType.Properties[fieldDesc.GetJsonName()] = recursedJSONSchemaType
		if required {
			jsonSchemaType.Required = append(jsonSchemaType.Required, fieldDesc.GetJsonName())
		}
	}
	return jsonSchemaType, nil
}

func formatDescription(sl *descriptor.SourceCodeInfo_Location) string {
	var lines []string
	for _, str := range sl.GetLeadingDetachedComments() {
		if s := strings.TrimSpace(str); s != "" {
			lines = append(lines, s)
		}
	}
	if s := strings.TrimSpace(sl.GetLeadingComments()); s != "" {
		lines = append(lines, s)
	}
	if s := strings.TrimSpace(sl.GetTrailingComments()); s != "" {
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n\n")
}
