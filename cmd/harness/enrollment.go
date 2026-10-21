package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
)

func readBatchEnrollment(config *batchConfig, targets []campaign.Target) ([]byte, error) {
	path := config.Enrollment.Path
	if gate.Overlap(path, config.Directory) {
		return nil, errors.New("enrollment source overlaps batch output")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		file.Close()
		return nil, errors.New("enrollment must be a regular file of at most 1 MiB")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, err
	}
	if len(data) > 1<<20 || gate.Digest(data) != config.Enrollment.SHA256 {
		return nil, errors.New("enrollment source digest mismatch")
	}
	var enrollment round.Enrollment
	if err := round.Decode(data, &enrollment); err != nil {
		return nil, err
	}
	if err := enrollment.Validate(targets); err != nil {
		return nil, err
	}
	config.enrollmentMaterials, err = round.ReadProtocolMaterials(enrollment.Protocol, config.Directory)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// prepareBatchEnrollment 绑定实际计划，只发布元数据，不向模型或门禁提供参考正文。
func prepareBatchEnrollment(config batchConfig, backend execution.Executor) func(context.Context, campaign.Artifact) error {
	if config.Enrollment == nil {
		return nil
	}
	return func(ctx context.Context, plan campaign.Artifact) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(plan.Path)
		if err != nil {
			return err
		}
		if gate.Digest(data) != plan.SHA256 {
			return errors.New("persisted campaign plan digest mismatch")
		}
		var enrollment round.Enrollment
		if err := round.Decode(config.enrollmentData, &enrollment); err != nil {
			return err
		}
		manifest, err := enrollment.Bind(data)
		if err != nil {
			return err
		}
		root := config.Directory
		if manifest.Protocol != nil {
			if backend == nil || backend.Info() != manifest.Protocol.Execution {
				return errors.New("actual execution backend differs from the frozen protocol")
			}
			if len(config.enrollmentMaterials) != len(manifest.Protocol.Materials) {
				return errors.New("protocol material snapshots are required before publication")
			}
			if err := os.Mkdir(filepath.Join(root, "protocol-materials"), 0700); err != nil {
				return err
			}
			if err := syncBatchDirectory(root); err != nil {
				return err
			}
			for name, material := range manifest.Protocol.Materials {
				data := config.enrollmentMaterials[name]
				if gate.Digest(data) != material.SHA256 {
					return errors.New("protocol material snapshot digest mismatch")
				}
				if err := writeBatchFile(filepath.Join(root, "protocol-materials", name), data); err != nil {
					return err
				}
			}
		}
		if err := writeBatchFile(filepath.Join(root, "enrollment.json"), config.enrollmentData); err != nil {
			return err
		}
		manifestPath := filepath.Join(root, "round-manifest.json")
		if err := writeBatchJSON(manifestPath, manifest); err != nil {
			return err
		}
		return writeBatchJSON(filepath.Join(root, "round-request.json"), round.Request{
			ManifestPath: manifestPath, CampaignDir: filepath.Dir(plan.Path), EvidenceDir: filepath.Join(root, "round"), References: []round.Reference{},
		})
	}
}
