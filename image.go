package containerd

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/rootfs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type Image interface {
	Name() string
	Target() ocispec.Descriptor

	Unpack(context.Context, string) error
}

var _ = (Image)(&image{})

type image struct {
	client *Client

	i images.Image
}

func (i *image) Name() string {
	return i.i.Name
}

func (i *image) Target() ocispec.Descriptor {
	return i.i.Target
}

func (i *image) Unpack(ctx context.Context, snapshotterName string) error {
	layers, err := i.getLayers(ctx)
	if err != nil {
		return err
	}

	sn := i.client.SnapshotService(snapshotterName)
	a := i.client.DiffService()
	cs := i.client.ContentStore()

	var chain []digest.Digest
	for _, layer := range layers {
		unpacked, err := rootfs.ApplyLayer(ctx, layer, chain, sn, a)
		if err != nil {
			// TODO: possibly wait and retry if extraction of same chain id was in progress
			return err
		}
		if unpacked {
			info, err := cs.Info(ctx, layer.Blob.Digest)
			if err != nil {
				return err
			}
			if info.Labels["uncompressed"] != layer.Diff.Digest.String() {
				if info.Labels == nil {
					info.Labels = map[string]string{}
				}
				info.Labels["uncompressed"] = layer.Diff.Digest.String()
				if err := cs.Update(ctx, info, "labels.uncompressed"); err != nil {
					return err
				}
			}
		}

		chain = append(chain, layer.Diff.Digest)
	}

	return nil
}

func (i *image) getLayers(ctx context.Context) ([]rootfs.Layer, error) {
	cs := i.client.ContentStore()

	// TODO: Support manifest list
	p, err := content.ReadBlob(ctx, cs, i.i.Target.Digest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read manifest blob")
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal manifest")
	}
	diffIDs, err := i.i.RootFS(ctx, cs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve rootfs")
	}
	if len(diffIDs) != len(manifest.Layers) {
		return nil, errors.Errorf("mismatched image rootfs and manifest layers")
	}
	layers := make([]rootfs.Layer, len(diffIDs))
	for i := range diffIDs {
		layers[i].Diff = ocispec.Descriptor{
			// TODO: derive media type from compressed type
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    diffIDs[i],
		}
		layers[i].Blob = manifest.Layers[i]
	}
	return layers, nil
}
