.PHONY: catbox

DATE=`date +%c`
GIT_ID=`git rev-parse --short HEAD`

catbox:
	sed -i -e "s/^const CreatedDate.*/const CreatedDate = \"$(DATE)\"/" version.go
	sed -i -e "s/^const Version.*/const Version = \"catbox-$(GIT_ID)\"/" version.go
	go build
	sed -i -e "s/^const CreatedDate.*/const CreatedDate = \"\"/" version.go
	sed -i -e "s/^const Version.*/const Version = \"\"/" version.go
