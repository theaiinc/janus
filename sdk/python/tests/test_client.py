import json
import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parents[1]))

from janus_client import Emitter, Receiver


class Response:
    def __init__(self, payload):
        self.payload = payload

    def read(self):
        return json.dumps(self.payload).encode()


class ClientTests(unittest.TestCase):
    @patch("urllib.request.urlopen")
    def test_emitter_and_receiver_use_namespace_and_alias_routes(self, urlopen):
        urlopen.return_value = Response({"namespace": "team one", "alias": "events"})
        emitter = Emitter("http://janus.local/", mode="proxy")
        receiver = Receiver("http://janus.local/", mode="proxy")

        emitter.register("team one", "events", {"localUrl": "http://origin"})
        receiver.receive("team one", "events", "/stream")

        first, second = [call.args[0] for call in urlopen.call_args_list]
        self.assertEqual(
            first.full_url,
            "http://janus.local/api/namespaces/team%20one/aliases/events",
        )
        self.assertEqual(first.method, "PUT")
        self.assertEqual(
            second.full_url,
            "http://janus.local/api/namespaces/team%20one/aliases/events/data/stream",
        )
        self.assertEqual(second.method, "GET")

    @patch("urllib.request.urlopen")
    def test_receiver_resolves_and_connects_directly(self, urlopen):
        endpoint = Response({"url": "https://tunnel.example/base"})
        final = Response({"ok": True})
        urlopen.side_effect = [endpoint, final]
        receiver = Receiver("http://janus.local/")

        receiver.receive("team", "events", "stream?x=1")

        first, second = [call.args[0] for call in urlopen.call_args_list]
        self.assertEqual(
            first.full_url,
            "http://janus.local/api/namespaces/team/aliases/events/endpoint",
        )
        self.assertEqual(second.full_url, "https://tunnel.example/base/stream?x=1")


if __name__ == "__main__":
    unittest.main()
