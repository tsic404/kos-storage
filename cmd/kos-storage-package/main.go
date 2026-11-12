// Command kos-storage-package builds the storage extension artifact: the
// package the SDK's single packing path produces (SDK-C-4) and, on request, the
// declaration document a system component installs (KOSD-C-17).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsic404/kosd/pkg/extension"

	"github.com/tsic404/kos-storage/pkg/storage"
)

func main() {
	outDir := flag.String("out", "dist", "directory the package archive is written to")
	installDoc := flag.String("install-doc", "", "path the kernel install document is written to")
	flag.Parse()
	if err := run(*outDir, *installDoc); err != nil {
		fmt.Fprintln(os.Stderr, "kos-storage-package:", err)
		os.Exit(1)
	}
}

// run packs the manifest, writes the archive, and writes the install document
// when a path is given.
func run(outDir, installDocPath string) error {
	pkg, archive, err := storage.Package()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	packagePath := filepath.Join(outDir, packageName())
	if err := os.WriteFile(packagePath, archive, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", packagePath, err)
	}
	fmt.Printf("package %s\ndigest  %s\n", packagePath, pkg.Digest)

	if installDocPath == "" {
		return nil
	}
	content, err := storage.InstallArtifact(pkg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(installDocPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", installDocPath, err)
	}
	fmt.Printf("install %s\ndigest  %s\n", installDocPath, extension.DigestOf(content))
	return nil
}

// packageName is the artifact's file name: identity and version, so two
// versions never overwrite each other.
func packageName() string {
	return storage.Identifier + "-" + storage.Version + ".tar.gz"
}
