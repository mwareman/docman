package dock

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The TPRM case: the container's Config carries the old image's ENV (including
// TPRM_VERSION=1.0.26), LABELs and HEALTHCHECK merged in, plus settings the
// owner chose (TPRM_TRUST_PROXY, a changed TPRM_PORT, an extra label).
func TestDropImageDefaults(t *testing.T) {
	oldImage := decode(t, `{
		"Env": ["PATH=/opt/venv/bin:/usr/local/bin:/usr/bin", "TPRM_VERSION=1.0.26", "TPRM_PORT=8080", "TZ=UTC"],
		"Labels": {"org.opencontainers.image.version": "1.0.26", "org.opencontainers.image.title": "TPRM"},
		"Cmd": ["gunicorn", "-c", "gunicorn.conf.py", "app:app"],
		"WorkingDir": "/app",
		"User": "tprm",
		"Healthcheck": {"Test": ["CMD", "python", "-c", "check"], "Interval": 30000000000},
		"ExposedPorts": {"8080/tcp": {}},
		"Volumes": {"/data": {}}
	}`)
	body := decode(t, `{
		"Image": "tprm:latest",
		"Env": ["PATH=/opt/venv/bin:/usr/local/bin:/usr/bin", "TPRM_VERSION=1.0.26", "TPRM_PORT=9090", "TZ=UTC", "TPRM_TRUST_PROXY=true"],
		"Labels": {"org.opencontainers.image.version": "1.0.26", "org.opencontainers.image.title": "TPRM", "owner": "infosec"},
		"Cmd": ["gunicorn", "-c", "gunicorn.conf.py", "app:app"],
		"WorkingDir": "/app",
		"User": "tprm",
		"Healthcheck": {"Test": ["CMD", "python", "-c", "check"], "Interval": 30000000000},
		"ExposedPorts": {"8080/tcp": {}, "9090/tcp": {}},
		"Volumes": {"/data": {}},
		"Hostname": "tprm"
	}`)

	dropped := DropImageDefaults(body, oldImage)

	if got, want := strList(body["Env"]), []string{"TPRM_PORT=9090", "TPRM_TRUST_PROXY=true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v, want %v (inherited image entries dropped, owner's kept)", got, want)
	}
	if got := toStrMap(body["Labels"]); !reflect.DeepEqual(got, map[string]string{"owner": "infosec"}) {
		t.Fatalf("labels = %v", got)
	}
	if body["Cmd"] != nil {
		t.Fatalf("Cmd should fall back to the new image's: %v", body["Cmd"])
	}
	for _, k := range []string{"WorkingDir", "User", "Healthcheck", "Volumes"} {
		if _, ok := body[k]; ok {
			t.Fatalf("%s should fall back to the new image's", k)
		}
	}
	if ports := body["ExposedPorts"].(map[string]any); len(ports) != 1 || ports["9090/tcp"] == nil {
		t.Fatalf("exposed ports = %v (the owner's 9090 kept)", ports)
	}
	if body["Image"] != "tprm:latest" || body["Hostname"] != "tprm" {
		t.Fatal("unrelated settings must be kept")
	}
	sort.Strings(dropped)
	if len(dropped) < 8 {
		t.Fatalf("dropped = %v", dropped)
	}
}

// The owner's own choices survive even when they look like image settings.
func TestDropImageDefaultsKeepsOverrides(t *testing.T) {
	oldImage := decode(t, `{"Env": ["MODE=prod"], "Cmd": ["serve"], "User": "app", "WorkingDir": "/app"}`)
	body := decode(t, `{"Env": ["MODE=debug"], "Cmd": ["serve", "--verbose"], "User": "root", "WorkingDir": "/srv"}`)
	DropImageDefaults(body, oldImage)
	if !reflect.DeepEqual(strList(body["Env"]), []string{"MODE=debug"}) || strAt(body, "User") != "root" ||
		strAt(body, "WorkingDir") != "/srv" || !reflect.DeepEqual(strList(body["Cmd"]), []string{"serve", "--verbose"}) {
		t.Fatalf("overrides were changed: %v", body)
	}
}

// The Engine ignores the image's Cmd once a container has its own entrypoint, so
// a Cmd equal to the image's is kept then; it follows the image only when the
// entrypoint does too.
func TestDropImageDefaultsCmdWithOwnEntrypoint(t *testing.T) {
	oldImage := decode(t, `{"Entrypoint": ["/init"], "Cmd": ["serve"]}`)
	body := decode(t, `{"Entrypoint": ["/my-wrapper"], "Cmd": ["serve"]}`)
	DropImageDefaults(body, oldImage)
	if !reflect.DeepEqual(strList(body["Cmd"]), []string{"serve"}) {
		t.Fatalf("Cmd was dropped although the entrypoint is the owner's: %v", body["Cmd"])
	}

	body = decode(t, `{"Entrypoint": ["/init"], "Cmd": ["serve"]}`)
	DropImageDefaults(body, oldImage)
	if body["Entrypoint"] != nil || body["Cmd"] != nil {
		t.Fatalf("image entrypoint and command should both be left to the image: %v", body)
	}
}

// Without the old image's configuration nothing changes.
func TestDropImageDefaultsNoImage(t *testing.T) {
	body := decode(t, `{"Env": ["A=1"]}`)
	if d := DropImageDefaults(body, nil); d != nil || !reflect.DeepEqual(strList(body["Env"]), []string{"A=1"}) {
		t.Fatal("body changed without an image configuration")
	}
}

// A spec edit (applyConfig) sets Labels as map[string]string: handled too.
func TestDropImageDefaultsStringLabels(t *testing.T) {
	body := map[string]any{"Labels": map[string]string{"v": "1", "mine": "x"}}
	DropImageDefaults(body, map[string]any{"Labels": map[string]any{"v": "1"}})
	if !reflect.DeepEqual(body["Labels"], map[string]string{"mine": "x"}) {
		t.Fatalf("labels = %v", body["Labels"])
	}
}
