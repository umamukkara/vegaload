# ftp_transfer.py -- the same scenario as ftp-transfer.vl.js, in Python.
#
# You need an FTP server on localhost:21 and an account that may write
# /upload. Put the password in an environment variable. Python scenarios
# need python3.
#   FTP_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env FTP_PASSWORD ftp_transfer.py
#
# A call does not raise when the server fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

URL = "ftp://localhost:21"


def iteration():
    login = dict(username="vegaload", password=env.FTP_PASSWORD, allow_writes=True)

    # {id} is a new name on every call. roundtrip deletes that file.
    rt = ftp.roundtrip(URL, **login, path="/upload/vl-{id}.bin", size="1MiB")
    check(rt, {"file roundtrip": lambda r: r.ok and r.size == 1048576})

    # Change the path and expect_size to a file that is already on the server.
    listed = ftp.list(URL, username="vegaload", password=env.FTP_PASSWORD, path="/upload")
    check(listed, {"upload listed": lambda r: r.ok})
    got = ftp.download(
        URL,
        username="vegaload",
        password=env.FTP_PASSWORD,
        path="/upload/readme.txt",
        expect_size="12",
    )
    check(got, {"readme downloaded": lambda r: r.ok and r.size == 12})
