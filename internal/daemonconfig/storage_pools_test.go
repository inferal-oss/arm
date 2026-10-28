package daemonconfig

import "testing"

func TestStoragePoolConfigurationStrictAndHashed(t *testing.T) {
	h := configHome(t)
	configFile(t, h, "schema = 1\n[[storage_pools]]\nname = 'fast'\npath = '/mnt/fast'\ntier = 'build'\nceiling = '20GiB'\n")
	r, e := Resolve(h, defaults(), Overrides{})
	if e != nil || len(r.Settings.StoragePools) != 1 || r.Settings.StoragePools[0].Ceiling != 20<<30 {
		t.Fatal(r, e)
	}
	changed := r.Settings
	changed.StoragePools[0].Tier = "bulk"
	hash, e := SnapshotSHA256(changed)
	if e != nil || hash == r.SettingsSHA256 {
		t.Fatal("pool omitted from configuration binding", e)
	}
	for _, raw := range []string{"name='default'\npath='/mnt/x'", "name='Fast'\npath='/mnt/x'", "name='fast'\npath='~/tmp'", "name='fast'\npath='/mnt/x'\nunknown=1", "name='fast'\npath='/mnt/x'\nceiling='-1'"} {
		configFile(t, h, "schema=1\n[[storage_pools]]\n"+raw+"\n")
		if _, e = Resolve(h, defaults(), Overrides{}); e == nil {
			t.Fatal(raw)
		}
	}
}
