package dashboard

import (
    "errors"

    "github.com/rfdetoni/nodexlm/internal/local"
)

// resolveDraftModel ensures that a draft cannot refer to a path outside
// the managed GGUF library or to a missing shard.
func (m *Manager) resolveDraftModel(s Settings) (string, error) {
    if s.SpeculativeMode != "draft" { return "", nil }
    if s.DraftModel == "" || s.DraftModel == s.Model {
        return "", errors.New("draft model must differ from the main model")
    }
    path, err := modelPath(m.modelDir, s.DraftModel)
    if err != nil { return "", err }
    if _, err = local.ModelSize(path); err != nil { return "", err }
    return path, nil
}
