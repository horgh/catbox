# Releasing
* Bump values in `version.go`
* Update `CHANGELOG.md`
* `git commit -m vx.y.z`
* `git push`
* `git tag -a vx.y.z -m vx.y.z`
* `export GITHUB_TOKEN=tokenhere`
* `goreleaser`
* Don't need to push tags. goreleaser does it.
