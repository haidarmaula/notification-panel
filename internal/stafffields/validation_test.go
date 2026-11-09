package stafffields

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTextUsesUnicodeCharacterLimit(t *testing.T) {
	t.Parallel()

	assert.NoError(t, ValidateText(strings.Repeat("界", MaxTextLength), ""))
	assert.NoError(t, ValidateText("", strings.Repeat("é", MaxTextLength)))
	assert.ErrorIs(t, ValidateText(strings.Repeat("界", MaxTextLength+1), ""), ErrTextTooLong)
	assert.ErrorIs(t, ValidateText("", strings.Repeat("é", MaxTextLength+1)), ErrTextTooLong)
}
