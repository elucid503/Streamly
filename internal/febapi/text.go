package febapi

import "streamly/internal/textutil"

func DecodeText(value string) string {

	return textutil.DecodeHTML(value)

}
