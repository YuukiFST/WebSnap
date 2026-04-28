from app import BrowserManager


def test_browser_manager_singleton():
    bm1 = BrowserManager()
    bm2 = BrowserManager()
    assert bm1 is bm2


def test_browser_manager_launch_and_cleanup():
    bm = BrowserManager()
    pw, browser, context, page = bm.launch()
    assert pw is not None
    assert browser is not None
    assert context is not None
    assert page is not None
    page.goto('data:text/html,<h1>test</h1>')
    content = page.content()
    assert '<h1>test</h1>' in content
    bm.cleanup(pw, browser, context, page)


def test_browser_manager_multiple_launches():
    """Each launch creates a fresh browser in the same thread."""
    bm = BrowserManager()
    pw1, br1, ctx1, page1 = bm.launch()
    page1.goto('data:text/html,<p>first</p>')
    assert '<p>first</p>' in page1.content()
    bm.cleanup(pw1, br1, ctx1, page1)

    pw2, br2, ctx2, page2 = bm.launch()
    page2.goto('data:text/html,<p>second</p>')
    assert '<p>second</p>' in page2.content()
    bm.cleanup(pw2, br2, ctx2, page2)
