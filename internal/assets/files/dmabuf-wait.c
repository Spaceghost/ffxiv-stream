/*
 * dmabuf-wait: make Sunshine wait for a captured frame to finish being
 * written before it reads it.
 *
 * Sunshine captures sway through wlr-screencopy into dma-bufs. sway queues the
 * copy on the GPU, attaches its completion fence to the dma-buf (implicit
 * sync) and reports the frame ready. Mesa honours that fence when the buffer
 * is imported; NVIDIA's EGL does not, so under GPU load (a running game)
 * Sunshine reads the buffer before the copy lands - a fresh buffer, so a black
 * frame. That was the stream flicker.
 *
 * poll(POLLIN) on a dma-buf fd blocks until its pending writes are done, so
 * waiting there just before eglCreateImage() restores the ordering. Sunshine
 * loads EGL through glad with dlopen()/dlsym(), which is why the hook sits on
 * dlsym and eglGetProcAddress rather than on eglCreateImage alone.
 *
 * Loaded only into Sunshine, via LD_PRELOAD in /usr/local/bin/ffxiv-sunshine.
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <poll.h>
#include <stdint.h>
#include <string.h>

typedef void *EGLDisplay, *EGLContext, *EGLImage, *EGLClientBuffer;
typedef unsigned int EGLenum;
typedef intptr_t EGLAttrib;
typedef int32_t EGLint;

#define EGL_NONE                  0x3038
#define EGL_LINUX_DMA_BUF_EXT     0x3270
#define EGL_DMA_BUF_PLANE0_FD_EXT 0x3272
#define WAIT_MS 100 /* never stall capture for longer than this */

typedef EGLImage (*create_image_fn)(EGLDisplay, EGLContext, EGLenum,
                                    EGLClientBuffer, const EGLAttrib *);
typedef EGLImage (*create_image_khr_fn)(EGLDisplay, EGLContext, EGLenum,
                                        EGLClientBuffer, const EGLint *);
typedef void *(*get_proc_fn)(const char *);
typedef void *(*dlsym_fn)(void *, const char *);

static create_image_fn real_create_image;
static create_image_khr_fn real_create_image_khr;
static get_proc_fn real_get_proc;
static dlsym_fn real_dlsym;

static void wait_written(int fd)
{
    struct pollfd p = { .fd = fd, .events = POLLIN };
    if (fd >= 0)
        poll(&p, 1, WAIT_MS);
}

static EGLImage my_create_image(EGLDisplay d, EGLContext c, EGLenum target,
                                EGLClientBuffer b, const EGLAttrib *attrs)
{
    if (target == EGL_LINUX_DMA_BUF_EXT && attrs)
        for (int i = 0; attrs[i] != EGL_NONE; i += 2)
            if (attrs[i] == EGL_DMA_BUF_PLANE0_FD_EXT)
                wait_written((int)attrs[i + 1]);
    return real_create_image(d, c, target, b, attrs);
}

static EGLImage my_create_image_khr(EGLDisplay d, EGLContext c, EGLenum target,
                                    EGLClientBuffer b, const EGLint *attrs)
{
    if (target == EGL_LINUX_DMA_BUF_EXT && attrs)
        for (int i = 0; attrs[i] != EGL_NONE; i += 2)
            if (attrs[i] == EGL_DMA_BUF_PLANE0_FD_EXT)
                wait_written(attrs[i + 1]);
    return real_create_image_khr(d, c, target, b, attrs);
}

/* Swap in a wrapper for the functions we care about; pass the rest through. */
static void *wrap(const char *name, void *p)
{
    if (!p || !name)
        return p;
    if (!strcmp(name, "eglCreateImage")) {
        real_create_image = (create_image_fn)p;
        return (void *)my_create_image;
    }
    if (!strcmp(name, "eglCreateImageKHR")) {
        real_create_image_khr = (create_image_khr_fn)p;
        return (void *)my_create_image_khr;
    }
    return p;
}

static void *my_get_proc(const char *name)
{
    return wrap(name, real_get_proc(name));
}

void *dlsym(void *handle, const char *name)
{
    if (!real_dlsym)
        real_dlsym = (dlsym_fn)dlvsym(RTLD_NEXT, "dlsym", "GLIBC_2.34");
    void *p = real_dlsym(handle, name);
    if (p && name && !strcmp(name, "eglGetProcAddress")) {
        real_get_proc = (get_proc_fn)p;
        return (void *)my_get_proc;
    }
    return wrap(name, p);
}
