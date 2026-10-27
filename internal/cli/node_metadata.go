package cli

import "getarm.org/internal/model"

func (a *app) nodeMetadata(args []string) (int, error) {
	f := a.flags("node")
	host := f.String("host", "localhost", "local authority or registered direct peer ID/name")
	f.Bool("json", false, "JSON output (default: automatic)")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	var result model.NodeMetadata
	if err := a.hostCall(*host, "node_metadata", nil, &result); err != nil {
		return 1, err
	}
	return 0, a.print(result)
}
