package types_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	catalog "github.com/rancher/terraform-provider-rancher2/internal/ranchercatalog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func typeName(t reflect.Type) string {
	if t.Name() != "" {
		p := t.PkgPath()
		if p == "github.com/rancher/rancher/pkg/api/steve/catalog/types" || p == "github.com/rancher/terraform-provider-rancher2/internal/ranchercatalog" {
			switch t.Name() {
			case "ChartInstall", "ChartInstallAction", "ChartInfo", "ChartUpgradeAction", "ChartUpgrade", "ChartActionOutput":
				p = "$catalog"
			}
		}
		if p != "" {
			return p + "." + t.Name()
		}
		return t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeName(t.Elem())
	case reflect.Slice:
		return "[]" + typeName(t.Elem())
	case reflect.Map:
		return "map[" + typeName(t.Key()) + "]" + typeName(t.Elem())
	}
	return t.String()
}

func fill(v reflect.Value, empty bool) {
	switch v.Kind() {
	case reflect.String:
		if !empty {
			v.SetString("value")
		}
	case reflect.Bool:
		v.SetBool(!empty)
	case reflect.Int, reflect.Int64, reflect.Int32:
		if !empty {
			v.SetInt(42)
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), empty)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fill(v.Field(i), empty)
			}
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		if !empty {
			k := reflect.New(v.Type().Key()).Elem()
			fill(k, false)
			val := reflect.New(v.Type().Elem()).Elem()
			fill(val, false)
			v.SetMapIndex(k, val)
		}
	case reflect.Slice:
		n := 0
		if !empty {
			n = 1
		}
		v.Set(reflect.MakeSlice(v.Type(), n, n))
		if n > 0 {
			fill(v.Index(0), false)
		}
	case reflect.Interface:
		if !empty {
			v.Set(reflect.ValueOf(map[string]interface{}{"nested": []interface{}{"text", float64(42), true, nil}}))
		}
	}
}

func metadata(t reflect.Type) map[string]interface{} {
	fields := []map[string]interface{}{}
	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fields = append(fields, map[string]interface{}{"name": f.Name, "type": typeName(f.Type), "tag": string(f.Tag), "anonymous": f.Anonymous, "index": f.Index})
		}
	}
	methods := []map[string]interface{}{}
	for _, mt := range []reflect.Type{t, reflect.PointerTo(t)} {
		for i := 0; i < mt.NumMethod(); i++ {
			m := mt.Method(i)
			in, out := []string{}, []string{}
			for j := 0; j < m.Type.NumIn(); j++ {
				in = append(in, typeName(m.Type.In(j)))
			}
			for j := 0; j < m.Type.NumOut(); j++ {
				out = append(out, typeName(m.Type.Out(j)))
			}
			methods = append(methods, map[string]interface{}{"receiver": typeName(mt), "name": m.Name, "in": in, "out": out, "variadic": m.Type.IsVariadic()})
		}
	}
	return map[string]interface{}{"name": typeName(t), "fields": fields, "methods": methods}
}

func snapshot() (map[string]interface{}, error) {
	types := []reflect.Type{
		reflect.TypeOf(catalog.ChartInstall{}), reflect.TypeOf(catalog.ChartInstallAction{}),
		reflect.TypeOf(catalog.ChartInfo{}), reflect.TypeOf(catalog.ChartUpgradeAction{}),
		reflect.TypeOf(catalog.ChartUpgrade{}), reflect.TypeOf(catalog.ChartActionOutput{}),
	}
	result := map[string]interface{}{}
	for _, t := range types {
		fixtures := map[string]interface{}{}
		values := map[string]reflect.Value{"zero": reflect.New(t).Elem()}
		for _, mode := range []string{"empty", "populated"} {
			v := reflect.New(t).Elem()
			fill(v, mode == "empty")
			values[mode] = v
		}
		for i := 0; i < t.NumField(); i++ {
			v := reflect.New(t).Elem()
			fill(v.Field(i), false)
			values["field:"+t.Field(i).Name] = v
		}
		for name, v := range values {
			b, err := json.Marshal(v.Interface())
			if err != nil {
				return nil, fmt.Errorf("%s/%s marshal: %w", t.Name(), name, err)
			}
			decoded := reflect.New(t)
			if err := json.Unmarshal(b, decoded.Interface()); err != nil {
				return nil, fmt.Errorf("%s/%s unmarshal: %w", t.Name(), name, err)
			}
			back, err := json.Marshal(decoded.Interface())
			if err != nil {
				return nil, fmt.Errorf("%s/%s roundtrip marshal: %w", t.Name(), name, err)
			}
			fixtures[name] = map[string]interface{}{"json": string(b), "roundtrip": string(back)}
		}
		invalid := map[string]string{}
		invalidTypes := map[string]string{}
		invalidDetails := map[string]interface{}{}
		for _, s := range []string{"{", "[]", `{"timeout":17}`, `{"charts":"bad"}`} {
			v := reflect.New(t)
			err := json.Unmarshal([]byte(s), v.Interface())
			if err != nil {
				invalid[s] = err.Error()
				invalidTypes[s] = reflect.TypeOf(err).String()
				details := map[string]interface{}{"errorType": reflect.TypeOf(err).String(), "errorKind": reflect.TypeOf(err).Kind().String()}
				switch e := err.(type) {
				case *json.SyntaxError:
					details["offset"] = e.Offset
				case *json.UnmarshalTypeError:
					details["offset"] = e.Offset
					details["field"] = e.Field
					details["struct"] = e.Struct
					details["value"] = e.Value
					details["targetType"] = e.Type.String()
					details["targetKind"] = e.Type.Kind().String()
					details["targetName"] = e.Type.Name()
					details["targetPkgPath"] = e.Type.PkgPath()
				}
				invalidDetails[s] = details
			} else {
				invalid[s] = ""
			}
		}
		unmarshalFixtures := map[string]interface{}{}
		for name, input := range map[string]string{
			"null":            "null",
			"nilContainers":   "{\"values\":null,\"questions\":null,\"chart\":null,\"annotations\":null,\"charts\":null,\"operationTolerations\":null,\"timeout\":null}",
			"emptyContainers": "{\"values\":{},\"questions\":{},\"chart\":{},\"annotations\":{},\"charts\":[],\"operationTolerations\":[],\"timeout\":\"0s\"}",
		} {
			v := reflect.New(t)
			if err := json.Unmarshal([]byte(input), v.Interface()); err != nil {
				return nil, fmt.Errorf("%s/%s explicit unmarshal: %w", t.Name(), name, err)
			}
			encoded, err := json.Marshal(v.Interface())
			if err != nil {
				return nil, fmt.Errorf("%s/%s explicit roundtrip: %w", t.Name(), name, err)
			}
			states := map[string]interface{}{}
			for i := 0; i < t.NumField(); i++ {
				field := v.Elem().Field(i)
				switch field.Kind() {
				case reflect.Map, reflect.Slice:
					states[t.Field(i).Name] = map[string]interface{}{"nil": field.IsNil(), "length": field.Len()}
				case reflect.Pointer:
					states[t.Field(i).Name] = map[string]interface{}{"nil": field.IsNil()}
				}
			}
			unmarshalFixtures[name] = map[string]interface{}{"input": input, "roundtrip": string(encoded), "containerState": states}
		}
		result[t.Name()] = map[string]interface{}{"metadata": metadata(t), "fixtures": fixtures, "invalid": invalid, "invalid_types": invalidTypes, "invalid_details": invalidDetails, "unmarshal_fixtures": unmarshalFixtures}
	}
	result["external"] = []map[string]interface{}{metadata(reflect.TypeOf(v3.MapStringInterface{})), metadata(reflect.TypeOf(corev1.Toleration{})), metadata(reflect.TypeOf(metav1.Duration{}))}
	return result, nil
}

// Only the six private type owners changed; raw errors and external identities stay exact.
func errorTypeOwnership(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var snapshot map[string]interface{}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ChartInstall", "ChartInstallAction", "ChartInfo", "ChartUpgradeAction", "ChartUpgrade", "ChartActionOutput"} {
		contract := snapshot[name].(map[string]interface{})
		for _, raw := range contract["invalid_details"].(map[string]interface{}) {
			details := raw.(map[string]interface{})
			pkg := details["targetPkgPath"]
			if pkg != "github.com/rancher/rancher/pkg/api/steve/catalog/types" && pkg != "github.com/rancher/terraform-provider-rancher2/internal/ranchercatalog" {
				continue
			}
			switch details["targetName"] {
			case "ChartInstall", "ChartInstallAction", "ChartInfo", "ChartUpgradeAction", "ChartUpgrade", "ChartActionOutput":
				details["targetPkgPath"] = "$catalog"
			default:
				t.Fatalf("unexpected private JSON error target: %v", details)
			}
		}
	}
	return snapshot
}

func TestOriginalRancherCatalogContract(t *testing.T) {
	got, err := snapshot()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/original-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(errorTypeOwnership(t, actual), errorTypeOwnership(t, expected)) {
		t.Fatalf("catalog wire, raw JSON errors or reflection contract differs from the original Rancher fixture:\n%s", actual)
	}
}
