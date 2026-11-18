package install

import (
	"fmt"
	"os"
	"path/filepath"
)

func trustedImage(p *Plan, image string) error {
	imageInfo, err := os.Stat(image)
	if err != nil {
		return err
	}
	manifests := []*Manifest{p.Previous, &p.Manifest}
	if m, err := ReadManifest(p.ManifestPath); err == nil {
		manifests = append(manifests, m)
	}
	for _, m := range manifests {
		if m == nil || p.validateManifest(m) != nil {
			continue
		}
		for path, hash := range m.Files {
			if filepath.Dir(path) != filepath.Join(p.Root, "bin") {
				continue
			}
			// Compare file IDs, not spelling, to allow platform path aliases.
			recorded, err := os.Stat(path)
			if err != nil || !os.SameFile(imageInfo, recorded) {
				continue
			}
			data, exists, err := readRegular(path)
			if err != nil {
				return err
			}
			if exists && digest(data) == hash {
				return nil
			}
		}
	}
	return fmt.Errorf("executable is not an unchanged binary registered to this installation: %s", image)
}
