package theme

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

//go:embed builtins.json
var builtinsData []byte

var themeIdPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
var collectionIdPattern = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9.:-]{0,127}$`)
var futureRolePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{0,63}$`)
var seedPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Collection groups related imported appearance variants.
type Collection struct {
	Id    string `json:"id"`
	Label string `json:"label"`
}

// Definition is a fully normalized palette with optional paired appearance data.
type Definition struct {
	Id             string            `json:"id"`
	Label          string            `json:"label"`
	Appearance     string            `json:"appearance"`
	Colors         Colors            `json:"colors"`
	Variants       map[string]Colors `json:"variants,omitempty"`
	Collection     *Collection       `json:"collection,omitempty"`
	Managed        bool              `json:"managed,omitempty"`
	SidebarArtwork bool              `json:"sidebarArtwork,omitempty"`
}

// File is the portable version-one user theme representation.
type File struct {
	Version    int               `json:"version"`
	Id         string            `json:"id,omitempty"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Colors     Colors            `json:"colors"`
	Variants   map[string]Colors `json:"variants,omitempty"`
	Collection *Collection       `json:"collection,omitempty"`
	Managed    bool              `json:"managed,omitempty"`
}

func builtinData() ([]string, []Definition) {
	var data struct {
		Roles  []string     `json:"roles"`
		Themes []Definition `json:"themes"`
	}
	if err := json.Unmarshal(builtinsData, &data); err != nil {
		panic(err)
	}
	return data.Roles, data.Themes
}

// Roles returns all57 compatibility roles in their original order.
func Roles() []string { roles, _ := builtinData(); return roles }

// Builtins returns independent copies of the five pinned paired themes.
func Builtins() []Definition { _, themes := builtinData(); return themes }

func validLabel(value string) bool {
	length := len(utf16.Encode([]rune(strings.TrimSpace(value))))
	return length > 0 && length <= 48
}

func reservedId(identity string, isPublished bool) bool {
	for _, value := range []string{"system", "light", "dark", "t3-chat", "grove", "ocean", "ember", "iris", "t3-chat-dark", "t3-grove", "t3-ocean", "t3-ember", "t3-iris"} {
		if value == identity {
			return true
		}
	}
	return isPublished && identity == "t3-code"
}

func defaultColors(appearance string) Colors {
	flagship := Builtins()[0]
	colors := flagship.Colors
	if appearance == "dark" {
		colors = flagship.Variants["dark"]
	}
	for role, value := range colors {
		colors[role], _ = CanonicalColor(value)
	}
	return colors
}

func normalizeColors(values Colors, appearance string, isStrict bool) (Colors, error) {
	if isStrict && len(values) == 0 {
		return nil, errors.New("theme colors require at least one role")
	}
	colors := defaultColors(appearance)
	for role, value := range values {
		if _, exists := colors[role]; !exists {
			if isStrict {
				return nil, fmt.Errorf("unsupported theme role %q", role)
			}
			continue
		}
		canonical, err := CanonicalColor(value)
		if err != nil {
			if isStrict {
				return nil, err
			}
			continue
		}
		colors[role] = canonical
	}
	return colors, nil
}

// ParseFile strictly imports a user version-one theme; future-role publishing is separate.
func ParseFile(data []byte) (Definition, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return Definition{}, errors.New("theme file requires an object")
	}
	for _, key := range []string{"colors", "variants", "collection"} {
		if value, exists := fields[key]; exists && !strings.HasPrefix(strings.TrimSpace(string(value)), "{") {
			return Definition{}, fmt.Errorf("theme %s requires an object", key)
		}
	}
	if string(fields["managed"]) != "true" {
		delete(fields, "managed")
	}
	data, _ = json.Marshal(fields)
	var file File
	if json.Unmarshal(data, &file) != nil || file.Version != 1 || !validLabel(file.Name) || file.Appearance != "light" && file.Appearance != "dark" {
		return Definition{}, errors.New("invalid version-one theme metadata")
	}
	if file.Id == "" {
		if _, exists := fields["id"]; exists {
			return Definition{}, errors.New("explicit theme id must be valid")
		}
		file.Id = strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(file.Name), "-"), "-")
		if len(file.Id) > 48 {
			file.Id = file.Id[:48]
		}
		if file.Id == "" {
			file.Id = "custom-theme"
		}
	}
	if !themeIdPattern.MatchString(file.Id) || reservedId(file.Id, false) {
		return Definition{}, errors.New("invalid or reserved theme id")
	}
	if file.Collection != nil {
		if !collectionIdPattern.MatchString(file.Collection.Id) || !validLabel(file.Collection.Label) {
			return Definition{}, errors.New("invalid theme collection")
		}
		file.Collection.Label = strings.TrimSpace(file.Collection.Label)
	}
	colors, err := normalizeColors(file.Colors, file.Appearance, true)
	if err != nil {
		return Definition{}, err
	}
	variants := map[string]Colors{}
	for appearance, values := range file.Variants {
		if appearance == file.Appearance || appearance != "light" && appearance != "dark" {
			return Definition{}, errors.New("invalid or repeated base appearance variant")
		}
		variants[appearance], err = normalizeColors(values, appearance, true)
		if err != nil {
			return Definition{}, err
		}
	}
	return Definition{Id: file.Id, Label: strings.TrimSpace(file.Name), Appearance: file.Appearance, Colors: colors, Variants: variants, Collection: file.Collection, Managed: file.Managed}, nil
}

// SerializeFile emits all roles and paired variants in the portable v1 schema.
func SerializeFile(theme Definition) ([]byte, error) {
	colors, err := normalizeColors(theme.Colors, theme.Appearance, true)
	if err != nil {
		return nil, err
	}
	variants := map[string]Colors{}
	for appearance, values := range theme.Variants {
		variants[appearance], err = normalizeColors(values, appearance, true)
		if err != nil {
			return nil, err
		}
	}
	data, err := json.MarshalIndent(File{Version: 1, Id: theme.Id, Name: theme.Label, Appearance: theme.Appearance, Colors: colors, Variants: variants, Collection: theme.Collection, Managed: theme.Managed}, "", "  ")
	return append(data, '\n'), err
}

// PublishedFile is a lenient environment file whose filename owns identity.
type PublishedFile struct {
	Version    *int              `json:"version,omitempty"`
	Name       string            `json:"name"`
	Appearance string            `json:"appearance"`
	Canvas     string            `json:"canvas,omitempty"`
	Accent     string            `json:"accent,omitempty"`
	Colors     Colors            `json:"colors,omitempty"`
	Variants   map[string]Colors `json:"variants,omitempty"`
}

// ParsePublished accepts open future-role records while enforcing the original resource schema.
func ParsePublished(data []byte) (PublishedFile, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return PublishedFile{}, errors.New("published theme requires an object")
	}
	for _, key := range []string{"colors", "variants"} {
		if value, exists := fields[key]; exists && !strings.HasPrefix(strings.TrimSpace(string(value)), "{") {
			return PublishedFile{}, fmt.Errorf("published %s requires an object", key)
		}
	}
	if version, exists := fields["version"]; exists && string(version) != "1" {
		return PublishedFile{}, errors.New("unsupported published theme version")
	}
	var file PublishedFile
	if len(data) > 32*1024 || json.Unmarshal(data, &file) != nil || file.Version != nil && *file.Version != 1 || !validLabel(file.Name) || file.Appearance != "light" && file.Appearance != "dark" {
		return PublishedFile{}, errors.New("invalid published theme metadata or size")
	}
	file.Name = strings.TrimSpace(file.Name)
	for _, key := range []string{"canvas", "accent"} {
		if raw, exists := fields[key]; exists {
			var seed string
			if json.Unmarshal(raw, &seed) != nil || !seedPattern.MatchString(seed) {
				return PublishedFile{}, errors.New("invalid published seed")
			}
		}
	}
	if file.Canvas != "" && !seedPattern.MatchString(file.Canvas) || file.Accent != "" && !seedPattern.MatchString(file.Accent) {
		return PublishedFile{}, errors.New("published seeds require three or six digit hex")
	}
	if (file.Canvas == "" || file.Accent == "") && len(file.Colors) == 0 {
		return PublishedFile{}, errors.New("published theme is colorless")
	}
	records := []Colors{file.Colors}
	for appearance, colors := range file.Variants {
		if appearance != "light" && appearance != "dark" {
			delete(file.Variants, appearance)
			continue
		}
		if colors == nil {
			return PublishedFile{}, errors.New("published variant requires a role object")
		}
		records = append(records, colors)
	}
	for _, colors := range records {
		for role, value := range colors {
			if !futureRolePattern.MatchString(role) || len(utf16.Encode([]rune(strings.TrimSpace(value)))) > 64 || strings.TrimSpace(value) == "" {
				return PublishedFile{}, errors.New("invalid published role record")
			}
			colors[role] = strings.TrimSpace(value)
		}
	}
	return file, nil
}

// ResolvePublished derives seeds, drops unknown/bad roles and fills paired defaults.
func ResolvePublished(identity string, file PublishedFile) Definition {
	colors := defaultColors(file.Appearance)
	if file.Canvas != "" && file.Accent != "" {
		colors, _ = Generate(file.Appearance, file.Canvas, file.Accent)
	}
	for role, value := range file.Colors {
		if _, exists := colors[role]; exists {
			if canonical, err := CanonicalColor(value); err == nil {
				colors[role] = canonical
			}
		}
	}
	variants := map[string]Colors{}
	for appearance, values := range file.Variants {
		if appearance == file.Appearance {
			continue
		}
		variants[appearance], _ = normalizeColors(values, appearance, false)
	}
	return Definition{Id: identity, Label: file.Name, Appearance: file.Appearance, Colors: colors, Variants: variants, Managed: file.Canvas != "" && file.Accent != "" && len(file.Colors) == 0}
}
