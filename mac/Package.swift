// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "NotifyMe",
    platforms: [.macOS(.v13)],
    dependencies: [
        // Only used for RS256-signing the JWT in the Google Sheets
        // service-account OAuth flow (Security.framework's SecKey does
        // RSA signing, but not from a PKCS8 PEM without hand-rolled ASN.1
        // parsing — swift-crypto's _CryptoExtras does this reliably).
        .package(url: "https://github.com/apple/swift-crypto.git", from: "3.0.0")
    ],
    targets: [
        .executableTarget(
            name: "NotifyMe",
            dependencies: [
                .product(name: "Crypto", package: "swift-crypto"),
                .product(name: "_CryptoExtras", package: "swift-crypto"),
            ],
            path: "Sources/NotifyMe"
        )
    ]
)
