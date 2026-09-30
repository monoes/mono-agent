#!/bin/sh
# Stands in for mise in the #301 tests. Copied to a file named `mise`;
# shims are symlinks to it named after their tool. `mise bin-paths` lists
# the global installs' bin dirs. `mise which <tool>` and
# a shim pick the tool the way mise does for a node `path:` version:
# MISE_NODE_VERSION, then the nearest .tool-versions from the working
# directory up, then whatever is installed under $MISE_DATA_DIR/installs.
pick() {
	v=""
	case "$MISE_NODE_VERSION" in path:*) v="${MISE_NODE_VERSION#path:}" ;; esac
	d="$PWD"
	while [ -z "$v" ]; do
		if [ -f "$d/.tool-versions" ]; then
			v=$(sed -n 's/^node path://p' "$d/.tool-versions" | head -n 1)
			case "$v" in "" | /*) ;; *) v="$d/$v" ;; esac
		fi
		[ "$d" = / ] && break
		d=$(dirname "$d")
	done
	if [ -n "$v" ]; then
		echo "$v/bin/$1"
		return 0
	fi
	for p in "$MISE_DATA_DIR"/installs/*/*/bin/"$1"; do
		if [ -x "$p" ]; then
			echo "$p"
			return 0
		fi
	done
	echo "mise: $1 is not installed" >&2
	return 1
}

tool=$(basename "$0")
if [ "$tool" = mise ]; then
	case "$1" in
	which)
		pick "$2"
		exit $?
		;;
	bin-paths)
		# The global tools' bin dirs; FAKE_MISE_NO_BIN_PATHS stands in for
		# a mise that can't list them.
		[ -z "$FAKE_MISE_NO_BIN_PATHS" ] || exit 1
		for d in "$MISE_DATA_DIR"/installs/*/*/bin; do
			[ -d "$d" ] && echo "$d"
		done
		exit 0
		;;
	esac
	exit 2
fi
p=$(pick "$tool") || exit 1
exec "$p" "$@"
