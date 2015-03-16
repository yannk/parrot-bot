parrot-bot
==========

HTTP-IRC gateway

# Description

parrot connects to IRC and listen to incoming HTTP payloads. HTTP payloads are
messages relayed to IRC.

This is very useful in an engineering organisation as a multi-purpose
messaging bot. Specifically, it allows any application or system to talk to
its respective owners because anyone can speak HTTP.

# Usage

    $ make
    $ ./parrot --irc-server=irc.corp.com --http-address=:8080 --default-channel=ops
    $ curl http://localhost:8080/  # show further instructions

    $ curl -XPOST http://localhost:8080/post/ops -d "msg=INFO: The roof is on fire."

# History

I originally had implemented this in Perl using AnyEvent and it has been used at
Say Media for some time. Parrot is my first real Go project.

# Author

Yann Kerherve <yann.kerherve@gmail.com>

# Contributors

Ask Bjørn Hansen <ask@develooper.com>
