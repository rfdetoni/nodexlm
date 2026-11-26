#pragma once
#include <stdint.h>
#include "libNodexGo.h"

#ifdef __cplusplus
extern "C" {
#endif
int NodexMetalStart(const char *path);
void NodexMetalStop(void);
int NodexMetalRunning(void);
uint64_t NodexAvailableMemory(void);
#ifdef __cplusplus
}
#endif
