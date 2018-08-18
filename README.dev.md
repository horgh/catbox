# Releasing
* Bump values in `version.go`
* Update `CHANGELOG.md`
* Commit
* `git tag -a vx.y.z -m <commit>`
* `export GITHUB_TOKEN=tokenhere`
* `goreleaser`
* `git push --tags origin master`
