package dock

import (
	"reflect"
)

// DropImageDefaults removes from a container create body every setting that the
// container only has because its *old* image supplied it as a default, so that a
// recreate onto a new image picks up the new image's defaults instead.
//
// Docker's container inspect document merges the image's configuration into the
// container's: the image's ENV lines, LABELs, CMD, ENTRYPOINT, WORKDIR, USER,
// HEALTHCHECK, EXPOSE and VOLUME all appear in Config as if they had been set on
// the container. Copied verbatim into the replacement, they override the new
// image — a new image version then still reports the old version in its
// environment, runs the old command, or keeps the old health check.
//
// A setting counts as inherited when it is exactly the old image's value: an
// environment entry with the same KEY=value, a label with the same value, or a
// command / entrypoint / working directory / user / health check / stop signal /
// shell equal to the image's. Exposed ports and declared volumes the old image
// declared are dropped too (the anonymous volumes themselves are reattached by
// keepAnonymousVolumes). Anything the container's owner set differently is kept.
//
// oldImageConfig is the "Config" section of the old image's inspect document; nil
// leaves the body unchanged (for example when that image is gone).
func DropImageDefaults(body map[string]any, oldImageConfig map[string]any) (dropped []string) {
	if body == nil || oldImageConfig == nil {
		return nil
	}

	// Environment: drop KEY=value entries the old image set with that exact value.
	if env := strList(body["Env"]); len(env) > 0 {
		inherited := map[string]bool{}
		for _, e := range strList(oldImageConfig["Env"]) {
			inherited[e] = true
		}
		kept := make([]string, 0, len(env))
		for _, e := range env {
			if inherited[e] {
				dropped = append(dropped, "env "+keyOf(e))
				continue
			}
			kept = append(kept, e)
		}
		body["Env"] = kept
	}

	// Labels: drop those whose value is the old image's.
	if labels, ok := body["Labels"].(map[string]any); ok && len(labels) > 0 {
		imageLabels := toStrMap(oldImageConfig["Labels"])
		for k, v := range labels {
			if iv, has := imageLabels[k]; has {
				if s, isStr := v.(string); isStr && s == iv {
					delete(labels, k)
					dropped = append(dropped, "label "+k)
				}
			}
		}
	} else if labels, ok := body["Labels"].(map[string]string); ok && len(labels) > 0 {
		imageLabels := toStrMap(oldImageConfig["Labels"])
		for k, v := range labels {
			if iv, has := imageLabels[k]; has && iv == v {
				delete(labels, k)
				dropped = append(dropped, "label "+k)
			}
		}
	}

	// Lists: equal to the image's means "use the image default" (null for the Engine).
	// Entrypoint goes first: the Engine ignores the image's Cmd when a container
	// is given its own entrypoint, so Cmd is only left to the image when the
	// entrypoint is too — otherwise the container would start with no arguments.
	for _, key := range []string{"Entrypoint", "Cmd", "Shell"} {
		cur, img := strList(body[key]), strList(oldImageConfig[key])
		if key == "Cmd" && len(strList(body["Entrypoint"])) > 0 {
			continue
		}
		if len(cur) > 0 && len(img) > 0 && reflect.DeepEqual(cur, img) {
			body[key] = nil
			dropped = append(dropped, key)
		}
	}

	// Scalars.
	for _, key := range []string{"WorkingDir", "User", "StopSignal"} {
		cur, img := strAt(body, key), strAt(oldImageConfig, key)
		if cur != "" && cur == img {
			delete(body, key)
			dropped = append(dropped, key)
		}
	}

	// Health check.
	if hc, ok := body["Healthcheck"]; ok && hc != nil {
		if img, has := oldImageConfig["Healthcheck"]; has && img != nil && reflect.DeepEqual(normaliseJSON(hc), normaliseJSON(img)) {
			delete(body, "Healthcheck")
			dropped = append(dropped, "Healthcheck")
		}
	}

	// Exposed ports and declared volumes: keys the old image declared.
	for _, key := range []string{"ExposedPorts", "Volumes"} {
		cur, ok := body[key].(map[string]any)
		if !ok || len(cur) == 0 {
			continue
		}
		img, _ := oldImageConfig[key].(map[string]any)
		for k := range cur {
			if _, has := img[k]; has {
				delete(cur, k)
				dropped = append(dropped, key+" "+k)
			}
		}
		if len(cur) == 0 {
			delete(body, key)
		}
	}
	return dropped
}

func keyOf(env string) string {
	for i := 0; i < len(env); i++ {
		if env[i] == '=' {
			return env[:i]
		}
	}
	return env
}

// normaliseJSON makes decoded-JSON values comparable (numbers as float64 either way).
func normaliseJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = normaliseJSON(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = normaliseJSON(x)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return v
	}
}

// strList reads a string list from decoded JSON ([]any) or from a spec edit ([]string).
func strList(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	default:
		return toStrSlice(v)
	}
}
