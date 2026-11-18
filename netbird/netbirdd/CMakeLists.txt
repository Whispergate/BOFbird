
# Path to Go source
set(GO_SRC_DIR ${CMAKE_CURRENT_SOURCE_DIR})

# Go sources drive rebuilds. A DEPENDS on the directory does not track file
# modifications (directory mtime only changes on create/delete), so the build
# would silently reuse a stale binary. Glob the sources instead.
file(GLOB NETBIRDD_GO_SOURCES CONFIGURE_DEPENDS ${GO_SRC_DIR}/*.go)

# Output binary or DLL
set(GO_OUTPUT ${CMAKE_BINARY_DIR}/netbirdd.${ARCH}/netbirdd.${ARCH}.o)
set(GO_OUTPUT_EXE ${CMAKE_BINARY_DIR}/netbirdd.${ARCH}/netbirdd.exe)

if(${ARCH} MATCHES "x64")
    set(GO_COMPILER "x86_64-w64-mingw32-gcc")
    set(GO_ARCH "amd64")
else()
    set(GO_COMPILER "i686-w64-mingw32-gcc")
    set(GO_ARCH "386")
endif()

# BOF-PE build (c-shared DLL with beacon imports)
add_custom_command(
    OUTPUT ${GO_OUTPUT}
    COMMAND ${CMAKE_COMMAND} -E env
        GOOS=windows GOARCH=${GO_ARCH} CGO_ENABLED=1
        CC=${GO_COMPILER}
        CGO_CFLAGS=-I${CMAKE_SOURCE_DIR}/beacon
        CGO_LDFLAGS=-L$<TARGET_FILE_DIR:beacon>
        go build -trimpath -buildmode=c-shared -ldflags "-s -w" -tags "bofpe" -o ${GO_OUTPUT} .
    WORKING_DIRECTORY ${GO_SRC_DIR}
    DEPENDS ${NETBIRDD_GO_SOURCES} ${GO_SRC_DIR}/go.mod
    COMMENT "Building netbirdd.${ARCH}.o (Go c-shared, bofpe tag)"
    VERBATIM
)

# Standalone .exe build (no CGo beacon dependency)
add_custom_command(
    OUTPUT ${GO_OUTPUT_EXE}
    COMMAND ${CMAKE_COMMAND} -E env
        GOOS=windows GOARCH=${GO_ARCH}
        go build -trimpath -ldflags "-s -w" -o ${GO_OUTPUT_EXE} .
    WORKING_DIRECTORY ${GO_SRC_DIR}
    DEPENDS ${NETBIRDD_GO_SOURCES} ${GO_SRC_DIR}/go.mod
    COMMENT "Building netbirdd.exe (Go standalone)"
    VERBATIM
)

add_custom_target(netbirdd.${ARCH} ALL DEPENDS ${GO_OUTPUT})
add_custom_target(netbirdd_cmd.${ARCH} ALL DEPENDS ${GO_OUTPUT_EXE})
add_dependencies(netbirdd.${ARCH} beacon)

install(FILES ${GO_OUTPUT} DESTINATION "pe/")
install(FILES ${GO_OUTPUT_EXE} DESTINATION "${ARCH}/")
install(FILES ${CMAKE_CURRENT_SOURCE_DIR}/netbirdd_ost_bof.s1.py DESTINATION "ost/")
