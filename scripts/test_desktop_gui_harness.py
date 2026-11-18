"""Do not dispatch native window callbacks after the wait has completed."""
import unittest
from unittest import mock

from test_desktop_gui_gtk import close_window, wait_for


class NativeWaitTests(unittest.TestCase):
    def test_already_closed_window_does_not_pump_its_native_callbacks(self):
        context = mock.Mock()
        context.pending.side_effect = AssertionError("dispatched after native window destruction")
        window = mock.Mock(session=None)
        window.get_visible.return_value = False
        close_window(context, window)
        window.close.assert_called_once_with()
        context.pending.assert_not_called()
        context.iteration.assert_not_called()

    def test_asynchronous_close_stops_before_the_next_queued_native_callback(self):
        context = mock.Mock()
        window = mock.Mock(session=None)
        window.get_visible.return_value = True

        def dispatch(_blocking):
            window.get_visible.return_value = False
            context.pending.side_effect = AssertionError("dispatched after native window destruction")

        context.iteration.side_effect = dispatch
        close_window(context, window)
        context.iteration.assert_called_once_with(False)
        context.pending.assert_called_once_with()

    def test_unfinished_wait_keeps_its_timeout(self):
        context = mock.Mock()
        context.pending.return_value = False
        with self.assertRaisesRegex(AssertionError, "unfinished operation"):
            wait_for(context, lambda: False, "unfinished operation", timeout=.02)


if __name__ == "__main__":
    unittest.main()
