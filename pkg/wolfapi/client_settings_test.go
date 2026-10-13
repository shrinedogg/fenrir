package wolfapi_test

import (
	"encoding/json"
	"sort"
	"testing"

	"games-on-whales.github.io/direwolf/pkg/wolfapi"
)

// Wolf reads client_settings with reflect-cpp and no DefaultIfMissing: when
// the object is present, every field of wolf::config::ClientSettings is
// required. Keep this list in sync with src/moonlight-server/state/
// serialised_config.hpp in wolf; a missing key makes /api/v1/sessions/add
// reject the session ("Field named '<key>' not found").
var wolfRequiredClientSettings = []string{
	"controllers_override",
	"h_scroll_acceleration",
	"motion_controller_override",
	"mouse_acceleration",
	"run_gid",
	"run_uid",
	"v_scroll_acceleration",
}

func TestClientSettingsMarshalsEveryWolfRequiredField(t *testing.T) {
	raw, err := json.Marshal(wolfapi.ClientSettings{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range wolfRequiredClientSettings {
		if _, ok := got[key]; !ok {
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Errorf("client_settings JSON is missing %q (wolf requires it); got keys %v", key, keys)
		}
	}
}
