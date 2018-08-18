# Releasing
* Bump values in `version.go`
* Update `CHANGELOG.md`
* Commit
* `git push`
* `git tag -a vx.y.z -m <commit>`
* `export GITHUB_TOKEN=tokenhere`
* `goreleaser`
* Don't need to push tags I believe, goreleaser does it.
