"""Load a built plugin through its public C ABI and verify the store identity and page."""

import base64
import ctypes as c
import json
import sys
from pathlib import Path


class Buffer(c.Structure):
    _fields_ = [("ptr", c.c_void_p), ("size", c.c_size_t)]


Call = c.CFUNCTYPE(c.c_int, c.c_char_p, c.c_void_p, c.c_size_t, c.POINTER(Buffer))
Release = c.CFUNCTYPE(None, c.c_void_p, c.c_size_t)
Shutdown = c.CFUNCTYPE(None)


class Host(c.Structure):
    _fields_ = [("version", c.c_uint32), ("context", c.c_void_p),
                ("call", c.c_void_p), ("release", c.c_void_p)]


class Plugin(c.Structure):
    _fields_ = [("version", c.c_uint32), ("call", Call),
                ("release", Release), ("shutdown", Shutdown)]


def main():
    library = c.CDLL(str(Path(sys.argv[1]).resolve()))
    init = library.cliproxy_plugin_init
    init.argtypes = [c.POINTER(Host), c.POINTER(Plugin)]
    init.restype = c.c_int
    plugin = Plugin()
    assert init(c.byref(Host(1)), c.byref(plugin)) == 0 and plugin.version == 1

    def invoke(method, payload=None):
        raw = json.dumps(payload).encode()
        output = Buffer()
        assert plugin.call(method.encode(), raw, len(raw), c.byref(output)) == 0
        try:
            response = json.loads(c.string_at(output.ptr, output.size))
            assert response["ok"], response
            return response["result"]
        finally:
            plugin.release(output.ptr, output.size)

    registry = Path(__file__).resolve().parents[1] / "registry.json"
    entry, = json.loads(registry.read_text(encoding="utf-8"))["plugins"]
    registration = invoke("plugin.register")
    assert registration["capabilities"] == {"management_api": True}
    assert invoke("plugin.reconfigure") == registration
    metadata = registration["metadata"]
    assert metadata["GitHubRepository"] == entry["repository"]
    resource, = invoke("management.register")["Resources"]
    assert resource["Menu"] == entry["name"] == metadata["Name"]
    path = f"/v0/resource/plugins/{entry['id']}{resource['Path']}"
    page = invoke("management.handle", {"Method": "GET", "Path": path})
    assert page["StatusCode"] == 200 and "text/html" in page["Headers"]["Content-Type"][0]
    html = base64.b64decode(page["Body"]).decode()
    assert entry["name"] in html and f'v{metadata["Version"]}' in html
    assert invoke("management.handle", {"Method": "GET", "Path": "/"})["StatusCode"] == 404
    assert invoke("management.handle", {"Method": "POST", "Path": path})["StatusCode"] == 404
    invoke("plugin.quiesce")
    invoke("plugin.shutdown")
    plugin.shutdown()
    print(metadata["Version"])


if __name__ == "__main__":
    main()
