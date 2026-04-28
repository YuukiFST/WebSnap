from downloader import AsyncResourceClient


def test_download_many_parallel():
    client = AsyncResourceClient()

    urls = [
        'https://httpbin.org/get',
        'https://httpbin.org/get?foo=bar',
    ]
    results = client.download_many(urls)

    assert len(results) == 2
    for url in urls:
        assert url in results
        assert b'"url"' in results[url]


def test_download_many_invalid_url_skipped():
    client = AsyncResourceClient()

    urls = ['https://invalid.example.zzz/nonexistent']
    results = client.download_many(urls)

    assert len(results) == 0


def test_download_many_mixed_valid_invalid():
    client = AsyncResourceClient()

    urls = [
        'https://httpbin.org/get',
        'https://invalid.example.zzz/nonexistent',
    ]
    results = client.download_many(urls)

    assert len(results) == 1
    assert 'https://httpbin.org/get' in results
