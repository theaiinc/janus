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
    def test_proxy_routes_use_namespace_and_alias(self, urlopen):
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
        self.assertEqual(second.full_url, "http://janus.local/api/namespaces/team%20one/aliases/events/data/stream")

    @patch("urllib.request.urlopen")
    def test_receiver_resolves_direct_endpoint(self, urlopen):
        urlopen.side_effect = [
            Response({"url": "https://tunnel.example/base"}),
            Response({"ok": True}),
        ]
        receiver = Receiver("http://janus.local/")
        receiver.receive("team", "events", "stream?x=1")
        first, second = [call.args[0] for call in urlopen.call_args_list]
        self.assertEqual(first.full_url, "http://janus.local/api/namespaces/team/aliases/events/endpoint")
        self.assertEqual(second.full_url, "https://tunnel.example/base/stream?x=1")

    @patch("urllib.request.urlopen")
    def test_pairing_and_endpoint_cache(self, urlopen):
        urlopen.side_effect = [
            Response({"apiKey": "mobile-key", "tenant": "team"}),
            Response({"url": "https://tunnel.example", "expiresAt": "2099-01-01T00:00:00+00:00"}),
            Response({"ok": True}),
            Response({"ok": True}),
        ]
        receiver = Receiver("http://janus.local/")
        result = receiver.client.pair("PAIR-CODE")
        receiver.receive("team", "events", "one")
        receiver.receive("team", "events", "two")
        self.assertEqual(result["apiKey"], "mobile-key")
        self.assertEqual(urlopen.call_args_list[0].args[0].full_url, "http://janus.local/api/auth/pairing/exchange")
        self.assertEqual(len(urlopen.call_args_list), 4)


if __name__ == "__main__":
    unittest.main()
