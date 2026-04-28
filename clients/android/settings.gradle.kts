pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

// Foojay disco-API JDK download channel for Gradle's daemon-JVM
// criteria (gradle/gradle-daemon-jvm.properties). When the local
// machine lacks a matching JDK, Gradle resolves a download URL for
// the right vendor/version through this plugin instead of failing
// with "No defined toolchain download url for WINDOWS on x86_64."
plugins {
    id("org.gradle.toolchains.foojay-resolver-convention") version "1.0.0"
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "OnScreen"
include(":app")
