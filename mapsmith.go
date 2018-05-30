package mapsmith

import (
	"reflect"
	"strings"
	"sync"
)

const DefaultTag = "map"

func isStruct(v interface{}) bool {
	vv := reflect.ValueOf(v)
	return vv.Kind() == reflect.Struct || (vv.Kind() == reflect.Ptr && vv.Elem().Kind() == reflect.Struct)
}

func newStructAdapter(v interface{}) *structAdapter {
	return &structAdapter{T: reflect.TypeOf(v)}
}

type structAdapter struct {
	T reflect.Type
	V reflect.Value
}

func (a *structAdapter) Fields() []Field {
	max := a.V.NumField()
	fields := make([]Field, max)
	for i := 0; i < max; i++ {
		f := a.T.Field(i)
		v := a.V.Field(i)
		fields[i] = &fieldHelper{F: f, V: v}
	}

	return fields
}

type stringSet map[string]struct{}

func newStringSet(keys ...string) stringSet {
	ss := make(stringSet, len(keys))
	ss.Add(keys...)
	return ss
}

func (ss stringSet) Add(keys ...string) {
	for _, key := range keys {
		ss[key] = struct{}{}
	}
}

func (ss stringSet) Contains(key string) bool {
	_, ok := ss[key]
	return ok
}

func (ss stringSet) Keys() []string {
	keys := make([]string, 0, len(ss))
	for key := range ss {
		keys = append(keys, key)
	}

	return keys
}

type Field interface {
	Name() string
	Tag(name string) string
	IsZero() bool
	Kind() reflect.Kind
	Set(v interface{})
	Value() interface{}
	HasTag(name string) bool
}

type fieldHelper struct {
	V reflect.Value
	F reflect.StructField
}

func (f *fieldHelper) HasTag(name string) bool {
	_, ok := f.F.Tag.Lookup(name)
	return ok
}

func (f *fieldHelper) IsExported() bool {
	return f.F.PkgPath == ""
}

func (f *fieldHelper) Tag(name string) string {
	return f.F.Tag.Get(name)
}

func (f *fieldHelper) Kind() reflect.Kind {
	return f.V.Kind()
}

func (f *fieldHelper) Value() interface{} {
	return f.V.Interface()
}

func (f *fieldHelper) IsZero() bool {
	zero := reflect.Zero(f.F.Type).Interface()
	current := f.Value()
	return reflect.DeepEqual(current, zero)
}

func (f *fieldHelper) Set(v interface{}) {
	if !f.IsExported() {
		// TODO: return error
		return
	}

	next := reflect.ValueOf(v)
	if next.Kind() != f.V.Kind() {
		// TODO: error
		return
	}

	f.V.Set(next)
}

func (f *fieldHelper) Name() string {
	return f.F.Name
}

type FieldAdapter interface {
	Set(v interface{})
	Value() interface{}
	Kind() reflect.Kind
}

type MapFieldAdapter interface {
	SetIndex(index string, value interface{})
	Index(index string) interface{}
	Keys() []string
}

type mapFieldAdapter struct {
	Value reflect.Value
}

func (a *mapFieldAdapter) SetIndex(index string, value interface{}) {
	a.Value.SetMapIndex(reflect.ValueOf(index), reflect.ValueOf(value))
}

func (a *mapFieldAdapter) Index(index string) interface{} {
	value := a.Value.MapIndex(reflect.ValueOf(index))
	return value.Interface()
}

func (a *mapFieldAdapter) Keys() []string {
	var valueKeys []reflect.Value
	if a.Value.Kind() == reflect.Ptr {
		valueKeys = a.Value.Elem().MapKeys()
	} else {
		valueKeys = a.Value.MapKeys()
	}

	keys := make([]string, 0)
	for _, k := range valueKeys {
		ks, ok := k.Interface().(string)
		if ok {
			keys = append(keys, ks)
		}
	}

	return keys
}

type fieldInitializer struct {
	init     sync.Once
	instance interface{}
	target   Field
}

func (fi *fieldInitializer) ensureInit() {
	fi.init.Do(func() {
		fi.target.Set(fi.instance)
	})
}

type initializerAdapter struct {
	FieldAdapter
	initializer *fieldInitializer
}

func (a *initializerAdapter) Set(v interface{}) {
	a.initializer.ensureInit()
	a.FieldAdapter.Set(v)
}

type mapInitializerAdapter struct {
	MapFieldAdapter
	initializer *fieldInitializer
}

func (a *mapInitializerAdapter) SetIndex(index string, value interface{}) {
	a.initializer.ensureInit()
	a.MapFieldAdapter.SetIndex(index, value)
}

func parseNameAndFlags(field Field, tagName string) (string, stringSet) {
	tagValue := field.Tag("map")
	flags := strings.Split(tagValue, ",")
	name := ""
	if len(flags) > 0 {
		name = flags[0]
		flags = flags[1:]
	}

	if name == "" {
		name = field.Name()
	}

	return name, newStringSet(flags...)
}

func parseField(field Field, name string, nameTag string, filterTag string, flags stringSet) (map[string]FieldAdapter, MapFieldAdapter) {
	var defaultField MapFieldAdapter
	m := make(map[string]FieldAdapter)
	if len(flags) < 1 {
		m[name] = field
		return m, defaultField
	}

	if flags.Contains("omitempty") && field.IsZero() {
		return m, defaultField
	}

	if flags.Contains("inline") {
		if field.Kind() != reflect.Ptr && field.Kind() != reflect.Struct && field.Kind() != reflect.Map {
			return m, defaultField
		}

		isZero := field.IsZero()
		kind := field.Kind()
		innerValue := field.Value()
		fieldType := reflect.TypeOf(innerValue)
		instance := reflect.ValueOf(innerValue)
		if isZero {
			if kind == reflect.Ptr {
				instance = reflect.New(fieldType.Elem())
			} else if kind == reflect.Map {
				instance = reflect.MakeMap(fieldType)
			} else {
				instance = reflect.New(fieldType)
			}
		}

		if kind == reflect.Map {
			if instance.Kind() != reflect.Ptr {
				instance = reflect.Indirect(instance)
			}

			if defaultField != nil {
				// TODO: warn of overshadowing inner catch-all's
			}

			if fieldType.Key().Kind() != reflect.String {
				// TODO: warn we can't use this type of map as catch-all
			}

			defaultField = &mapInitializerAdapter{
				MapFieldAdapter: &mapFieldAdapter{Value: instance},
				initializer: &fieldInitializer{
					instance: instance.Interface(),
					target:   field,
				},
			}
		} else {
			innerInfo := GetMappings(instance.Interface(), nameTag, filterTag)
			for ink, inf := range innerInfo.Fields {
				// todo: warn of duplicate
				if isZero {
					m[ink] = &initializerAdapter{
						FieldAdapter: inf,
						initializer: &fieldInitializer{
							instance: instance.Interface(),
							target:   field,
						},
					}
				} else {
					m[ink] = inf
				}
			}
		}
	} else {
		m[name] = field
	}

	return m, defaultField
}

type Info struct {
	Fields map[string]FieldAdapter
	Extra  MapFieldAdapter
}

func GetMappings(v interface{}, nameTag string, filterTag string) *Info {
	if filterTag == "" {
		filterTag = nameTag
	}

	mi := &Info{
		Fields: make(map[string]FieldAdapter),
		Extra:  nil,
	}

	for _, field := range newStructAdapter(v).Fields() {
		if !field.HasTag(filterTag) {
			continue
		}

		name, flags := parseNameAndFlags(field, nameTag)
		if name != "-" {
			fields, defaultField := parseField(field, name, nameTag, filterTag, flags)
			if defaultField != nil {
				mi.Extra = defaultField
			}

			for k, v := range fields {
				mi.Fields[k] = v
			}
		}
	}

	return mi
}

func TaggedToMap(v interface{}, nameTag string, filterTag string) map[string]interface{} {
	info := GetMappings(v, nameTag, filterTag)
	m := make(map[string]interface{})
	for k, f := range info.Fields {
		srcValue := f.Value()
		value := srcValue
		if isStruct(srcValue) {
			value = ToMap(srcValue)
		}

		m[k] = value
	}

	if info.Extra != nil {
		for _, key := range info.Extra.Keys() {
			m[key] = info.Extra.Index(key)
		}
	}

	return m
}

func ToMap(v interface{}) map[string]interface{} {
	return TaggedToMap(v, DefaultTag, DefaultTag)
}

func TaggedFromMap(m map[string]interface{}, dest interface{}, nameTag string, filterTag string) {
	mappings := GetMappings(dest, nameTag, filterTag)
	for key, srcValue := range m {
		field, ok := mappings.Fields[key]
		if !ok {
			if mappings.Extra != nil {
				mappings.Extra.SetIndex(key, srcValue)
			}

			continue
		}

		destValue := srcValue
		fieldKind := field.Kind()
		if fieldKind == reflect.Struct || (fieldKind == reflect.Ptr && reflect.TypeOf(field.Value()).Elem().Kind() == reflect.Struct) {
			if srcMap, ok := srcValue.(map[string]interface{}); ok {
				if fieldKind == reflect.Ptr {
					destValue = reflect.New(reflect.TypeOf(field.Value()).Elem()).Interface()
				}

				FromMap(srcMap, destValue)
			}
		}

		field.Set(destValue)
	}
}

func FromMap(m map[string]interface{}, dest interface{}) {
	TaggedFromMap(m, dest, DefaultTag, DefaultTag)
}

func MapKeys(m map[string]interface{}, keyMap map[string]string) map[string]interface{} {
	mapped := make(map[string]interface{})
	for k, v := range m {
		if mappedKey, ok := keyMap[k]; ok {
			k = mappedKey
		}

		mapped[k] = v
	}

	return mapped
}

func Join(a map[string]interface{}, b map[string]interface{}) map[string]interface{} {
	c := make(map[string]interface{})
	for k, v := range a {
		c[k] = v
	}

	for k, v := range b {
		c[k] = v
	}

	return c
}

func FilterMap(m map[string]interface{}, allowedKeys []string) map[string]interface{} {
	var ok bool
	var v interface{}
	r := map[string]interface{}{}
	for _, fieldName := range allowedKeys {
		v, ok = m[fieldName]
		if ok {
			r[fieldName] = v
		}
	}

	return r
}
