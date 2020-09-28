#!/bin/sh

set +e

logf() {
	local fmt=$1; shift
	printf "\033[1mT375: $fmt\033[m\n" "$@"
	printf "\033[1mT375: $fmt\033[m\n" "$@" >> ~/t375-logs
}

if [ "$1" ];then
	git checkout -f "$1" || { logf "failed to check out $1"; exit 0; }
fi

logf "start commit=%s" "$(git rev-parse @)"

eval $(go env)
rm -rf ~/junit/
mkdir -p ~/junit/
${GOPATH}/bin/gotestsum --junitfile ~/junit/unit-tests.xml -- -race -short github.com/networkservicemesh/sdk/pkg/networkservice/common/heal -count=10
rc=$?

logf "end rc=%s commit=%s" "$rc" "$(git rev-parse @)"

git checkout -f - || { logf "checking out back failed"; exit 0 }

exit 0
