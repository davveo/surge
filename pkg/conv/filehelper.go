package conv

const FileHelperUID = "filehelper"

func IsFileHelper(uid string) bool {
	return uid == FileHelperUID
}
