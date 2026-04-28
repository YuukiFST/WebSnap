import io
import os
import zipfile
from downloader import zip_directory_to_memory


def test_zip_directory_to_memory_creates_valid_zip(temp_dir):
    file1 = os.path.join(temp_dir, 'hello.txt')
    with open(file1, 'w') as f:
        f.write('world')

    sub = os.path.join(temp_dir, 'subdir')
    os.makedirs(sub)
    file2 = os.path.join(sub, 'nested.txt')
    with open(file2, 'w') as f:
        f.write('data')

    buf = zip_directory_to_memory(temp_dir)

    assert isinstance(buf, io.BytesIO)
    buf.seek(0)

    with zipfile.ZipFile(buf, 'r') as zf:
        names = zf.namelist()
        assert 'hello.txt' in names
        assert 'subdir/nested.txt' in names
        assert zf.read('hello.txt') == b'world'
        assert zf.read('subdir/nested.txt') == b'data'


def test_zip_directory_to_memory_empty_dir(temp_dir):
    buf = zip_directory_to_memory(temp_dir)

    assert isinstance(buf, io.BytesIO)
    buf.seek(0)

    with zipfile.ZipFile(buf, 'r') as zf:
        assert len(zf.namelist()) == 0


def test_zip_directory_to_memory_buffer_seeked_to_zero(temp_dir):
    with open(os.path.join(temp_dir, 'a.txt'), 'w') as f:
        f.write('content')

    buf = zip_directory_to_memory(temp_dir)
    assert buf.tell() == 0
