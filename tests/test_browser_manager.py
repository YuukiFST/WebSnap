import threading
from app import BrowserManager


def test_browser_manager_singleton():
    bm1 = BrowserManager()
    bm2 = BrowserManager()
    assert bm1 is bm2


def test_browser_manager_start_and_healthy():
    bm = BrowserManager()
    bm.start()
    assert bm.healthy is True
    assert bm._browser is not None


def test_browser_manager_get_and_release_context():
    bm = BrowserManager()
    bm.start()
    ctx = bm.get_context()
    assert ctx is not None
    page = ctx.new_page()
    page.goto('data:text/html,<h1>test</h1>')
    content = page.content()
    assert '<h1>test</h1>' in content
    page.close()
    bm.release_context(ctx)
    assert True
