This test data is printed via the etgrep command, with non-core call frames
identified and stripped as follows:

    sed -i .bak -E -e 's#^  [0-9a-f]* ([^/ ]*\.[^/ ]*/|main\.).*#  deaddead redacted.mod/pkg.fn redacted.mod/pkg/fn.go:1#' ./*.txt

GOROOT identified and stripped as follows:

    sed -i .bak -e 's#\(  [0-9a-f]* [^ ]* \).*/src/\(.*\)#\1\2#' ./*.txt

Verified with:

    find . -type f -name '*.txt' | xargs cat | grep '^ ' | awk '{print $3}' | grep '\..*/' | sort | uniq -c

And double-checked with:

    find . -type f -name '*.txt*' | xargs cat | grep '^ ' | awk '{print $2}' | sort | uniq -c
    find . -type f -name '*.txt*' | xargs cat | grep '^ ' | awk '{print $3}' | sort | uniq -c
