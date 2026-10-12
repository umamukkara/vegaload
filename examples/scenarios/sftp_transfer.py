# sftp_transfer.py -- the same scenario as sftp-transfer.vl.js, in Python.
#
# You need an SFTP server on localhost:22 and an account that may write
# upload/. Put the password and the host key fingerprint in environment
# variables. Python scenarios need python3.
#   SFTP_PASSWORD=secret SFTP_HOST_KEY=SHA256:... vegaload run -vus 4 -duration 10s \
#     -env SFTP_HOST_KEY -secret-env SFTP_PASSWORD sftp_transfer.py
#
# A call does not raise when the server fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

URL = "sftp://localhost"


def iteration():
    login = dict(
        username="vegaload",
        password=env.SFTP_PASSWORD,
        host_key=env.SFTP_HOST_KEY,
        allow_writes=True,
    )

    # {id} is a new name on every call. roundtrip deletes that file.
    rt = sftp.roundtrip(URL, **login, path="upload/vl-{id}.bin", size="1MiB")
    check(rt, {"file roundtrip": lambda r: r.ok and r.size == 1048576})

    # Change the path and expect_size to a file that is already on the server.
    listed = sftp.list(
        URL,
        username="vegaload",
        password=env.SFTP_PASSWORD,
        host_key=env.SFTP_HOST_KEY,
        path="upload",
    )
    check(listed, {"upload listed": lambda r: r.ok})
    got = sftp.download(
        URL,
        username="vegaload",
        password=env.SFTP_PASSWORD,
        host_key=env.SFTP_HOST_KEY,
        path="upload/readme.txt",
        expect_size="12",
    )
    check(got, {"readme downloaded": lambda r: r.ok and r.size == 12})
