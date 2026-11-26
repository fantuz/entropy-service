#!/bin/sh
# Both installed GUI commands use the same Python modules and system interpreter.
case ${0##*/} in
    entropy-server-gui) view=server ;;
    entropy-client-gui) view=client ;;
    *) echo "Run entropy-server-gui or entropy-client-gui." >&2; exit 2 ;;
esac

if [ ! -x /usr/bin/python3 ]; then
    printf '%s\n' \
        "The desktop GUI needs the system Python 3 and GTK 4 bindings." \
        "Debian/Ubuntu: sudo apt install python3-gi python3-gi-cairo python3-cairo gir1.2-gtk-4.0" \
        "Fedora: sudo dnf install python3-gobject python3-cairo gtk4 gobject-introspection" >&2
    exit 2
fi

exec /usr/bin/python3 -B /usr/share/entropy-service/entropy-gui --view "$view" "$@"
